# Iridium 调研笔记

回答：托管和发现四个 agent、接 Cloudflare 要用到的协议事实。前置阅读：`docs/plan.md`。

- 2026-11-05 在 kumo2 上核实，没有调模型。
- “Rh”指 Rhodium 已录的事实（`/home/iridium/kumo/rhodium-src/Rhodium`，`docs/architecture.md` §6.2–6.4、`test/fixtures/`），版本写在后面。
- “实测”指本机这次跑过；“文档”“源码”指读的文件。
- ⚠ 标出和 Rhodium 当时不一样的地方。

## 1. 版本

| 程序 | 本机 | Rhodium 核实时 |
|---|---|---|
| Claude Code | 2.1.318 | 2.1.304–2.1.310 |
| codex-cli | 0.165.0（`/usr/local/bin/codex` 是 node 包装） | 0.162.1 |
| pi | 1.4.0（`@earendil-works/pi-coding-agent`，`dist/bundle/cli.js`） | 1.3.0 |
| dsh | 未装；读 `dsh-v0.2.2-rc.1`（2026-11-04） | 无 |
| cloudflared | 2026.10.0 | — |

## 2. 拉起者怎么认：子进程的环境变量

Iridium 读 `/proc/<pid>/environ`，按下表认出“谁拉起了这个会话”。

| agent | 给子进程的变量 | 怎么核实 |
|---|---|---|
| Claude | `CLAUDE_CODE_SESSION_ID`（会话 ID）、`CLAUDE_PID`（claude 进程）、`CLAUDECODE`、`CLAUDE_CODE_ENTRYPOINT`。Agent 工具的子任务跑在同一个 claude 进程里，命令拿到的是父会话的 ID 和 PID。另见 `CLAUDE_CODE_CHILD_SESSION=1`，含义未确认（这次的父会话在 `sessions/` 里是 `kind: bg`） | 实测：本调研跑在一个 Agent 子任务里，读自己的环境，对上 `~/.claude/sessions/<pid>.json`（2.1.318） |
| Codex | `CODEX_THREAD_ID`、`CODEX_SESSION_ID`（主线程上两者相同）、`CODEX_VERSION`、`CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS`；npm 包装另加 `CODEX_MANAGED_BY_NPM`、`CODEX_MANAGED_PACKAGE_ROOT`。`CODEX_CI` 只在模型调的命令里有（Rh `start-mid-turn`），用户 shell 命令里为空 | 实测 0.165.0：私有 app-server + 临时 `CODEX_HOME` + 临时线程，`thread/shellCommand` 跑 `env`，不调模型 |
| pi | 所有子进程：`AI_AGENT=pi`、`PI_CODING_AGENT=true`（不带会话）。只有模型调的 `bash` 工具：`PI_SESSION_ID`、`PI_SESSION_FILE`、`PI_PROVIDER`、`PI_MODEL`、`PI_REASONING_LEVEL`（0.82.0 起）。rpc 的 `bash` 和 `!` 命令里没有 | 实测 1.4.0：`pi --mode rpc` 不发 prompt，发 rpc `bash` 跑 `env`；会话变量读 `docs/environment-variables.md` 和 CHANGELOG |
| dsh | 模型调的 shell：`DSH_SESSION_ID`、`DSH_HOME`、`DSH_SHELL=1`、`DSH_PROFILE`、`DSH_PROFILE_DIR` | 源码文档 `packages/shell/shell-env/README.md`（v0.2.2-rc.1），未实测 |

- pi 一启动就改进程标题，`/proc/<pid>/cmdline` 只剩 `pi` 加空格（实测 1.4.0，同 Rh）。从 `/proc` 认 pi 不能看命令行。
- 所以 pi 拉起的会话只能认出“是 pi 拉起的”，要知道哪段 pi 会话，只有模型的 `bash` 工具里才有 `PI_SESSION_ID`。

## 3. Claude

| 项 | 事实 | 核实 |
|---|---|---|
| `--permission-mode` | 列出的选项：`acceptEdits`、`auto`、`bypassPermissions`、`manual`、`dontAsk`、`plan`。⚠ `default` 不在列表里，但仍被接受（隐藏的旧名）；`manual` 是新名 | 实测 2.1.318：`claude --help`；`--permission-mode bogus` 报 “Allowed choices are acceptEdits, auto, bypassPermissions, manual, dontAsk, plan.”；`default`、`manual` 加一个不存在的参数时只报那个参数，没跑模型 |
| 运行中切换 | 控制请求 `set_permission_mode`；要能切到 `bypassPermissions`，启动时加 `--allow-dangerously-skip-permissions` | Rh §6.2（2.1.305） |
| 追加系统提示 | `--append-system-prompt`；配 `--system-prompt-snapshot off`，否则恢复时用第一回合存下的旧提示 | 本机 `--help` 仍有两项；Rh 探针 10 |
| `~/.claude/sessions/<pid>.json` | 每个活着的 claude 一份：`pid`、`sessionId`、`cwd`、`startedAt`、`procStart`、`version`、`kind`（`interactive`、`bg`）、`entrypoint`、`status`（如 `busy`）、`name`、`updatedAt`；旁边还有 `<pid>.<hash>.key` | 实测：只读本机 2 份（2.1.318）。`claude -p` 子进程会不会写、写什么，这次没测 |

`get_context_usage` 应答（Rh `claude/cmd-context`，2.1.304）：

- `categories[]`：`id`、`name`、`tokens`、`kind`（`used`、`deferred`、`buffer`、`free`），`id` 有 `system_prompt`、`system_tools`、`system_tools_deferred`、`custom_agents`、`memory_files`、`skills`、`messages`、`autocompact_buffer`、`free_space`。
- `totalTokens`、`maxTokens`、`rawMaxTokens`、`percentage`、`autocompactSource`；`gridRows` 是终端画格子用的，不要。

`get_usage`（带 `skip_behaviors: true`，Rh `claude/cmd-usage-session`，2.1.310）：

- `rate_limits.limits[]`：`kind`（`session`、`weekly_all`、`weekly_scoped`）、`group`（`session`、`weekly`）、`percent`、`severity`、`resets_at`（ISO 时间）、`scope.model.display_name`、`is_active`。
- 旧字段 `rate_limits.five_hour`、`seven_day`：`utilization`、`resets_at`。
- `rate_limits.extra_usage`：`is_enabled`、`used_credits`、`monthly_limit`、`utilization`、`currency`、`decimal_places`。
- `session`：`total_cost_usd`、`model_usage.<模型>` 的 `inputTokens`、`outputTokens`、`cacheReadInputTokens`、`cacheCreationInputTokens`、`costUSD`、`contextWindow`。
- 还有 `subscription_type`、`rate_limits_available`。API key 登录时 `rate_limits` 为 null。
- 不在文档里，CLI 回 `Unsupported control request subtype` 时当作没有。

## 4. Codex

托管法沿用 Rh（ADR-0011、§6.4，0.162.1）：每段对话一个私有 `codex app-server --listen stdio://`，不连共享后台。

| 项 | 事实 | 核实 |
|---|---|---|
| 帧 | 每行一条 JSON，无 `"jsonrpc"`；先 `initialize {clientInfo}` 再 `initialized` 通知 | 实测 0.165.0 |
| 开线程 | `thread/start {cwd, model?, approvalPolicy, sandbox, developerInstructions}`；ID 由 Codex 给（UUIDv7） | 实测；Rh |
| 接着旧线程 | `thread/resume {threadId, approvalPolicy, sandbox, model?, developerInstructions, excludeTurns: true}`；⚠ 0.165 还能按 `path` 恢复。不带审批和沙箱会退回只读 | 0.165.0 `generate-ts` 的 `ThreadResumeParams`；Rh `restart-idle` |
| 发消息 | 一律 `turn/start {threadId, input, clientUserMessageId, model?, effort?, approvalPolicy, sandboxPolicy}`；回合在跑时它自己变成插话 | Rh `start-mid-turn` |
| 插话 | `turn/steer {threadId, input, expectedTurnId, clientUserMessageId}`；空闲时报 `no active turn to steer`。压缩回合不能插话 | 类型实测仍是这几个字段；Rh |
| 中断 | `turn/interrupt {threadId, turnId}`，回合以 `interrupted` 结束 | Rh `interrupt` |
| 审批 `approvalPolicy` | `untrusted`、`on-request`、`never`、⚠ 新增 `{granular:{sandbox_approval, rules, skill_approval, request_permissions, mcp_elicitations}}`。`untrusted` 在 `thread/start` 里仍收，应答原样回 | `AskForApproval.ts`；实测 `thread/start` 回 `approvalPolicy: "untrusted"`、`approvalsReviewer: "user"` |
| 审批开/关 | 关：`never` + `danger-full-access`；开：`untrusted` + `danger-full-access` | Rh |
| 审批请求 | `item/commandExecution/requestApproval`、`item/fileChange/requestApproval`、`item/permissions/requestApproval`、`mcpServer/elicitation/request`、`item/tool/requestUserInput`；答 `{decision: accept/decline}` 等；了结后 `serverRequest/resolved` | `ServerRequest.ts` 列表未变；Rh |
| 模型和思考强度 | `model/list`；`turn/start` 带 `model`、`effort`，本回合起一直有效。⚠ `ReasoningEffort` 类型现在是任意字符串；本机档位 `low`…`ultra`，`gpt-6-luna`、`gpt-5.6-luna` 没有 `ultra`，`gpt-5.5` 到 `xhigh`；默认 `gpt-6.1-sol` | 实测 `model/list`（临时 `CODEX_HOME`） |
| 压缩 | `thread/compact/start` → 一个回合，里面一个 `contextCompaction` 条目。⚠ 通知列表里有 `thread/compacted`，类型注释写着已弃用、改看条目 | `ContextCompactedNotification.ts`；Rh `compact` |
| 用量 | `thread/tokenUsage/updated {tokenUsage:{total,last,modelContextWindow}}`，每份 `totalTokens`、`inputTokens`（含缓存）、`cachedInputTokens`、`cacheWriteInputTokens`、`outputTokens`、`reasoningOutputTokens`；上下文 = `last.totalTokens / modelContextWindow` | 类型实测；Rh `turn` |
| 额度 | `account/rateLimits/read` → `rateLimits`、`rateLimitsByLimitId`，窗口 `{usedPercent, windowDurationMins, resetsAt(秒)}`。⚠ 新字段：`ordinaryUsageAllowed`、`rateLimitResetCredits`、`accountId`、`rateLimitUpsell`；快照多了 `limitName`、`individualLimit`、`spendControlReached`。⚠ 新方法 `account/usage/read`（token 用量）。推送 `account/rateLimits/updated` 是部分更新 | `GetAccountRateLimitsResponse.ts`、`RateLimitSnapshot.ts`；没登录时两个都报 “codex account authentication required …”（实测） |
| 追加指令 | `developerInstructions`（不用 `baseInstructions`，它整个替换）。接着旧线程时再给的不生效 | Rh `developer-resume`（0.165 未复测） |
| 写锁 | `$CODEX_HOME/thread-writer-locks/<线程>.lock` flock；被占时 `thread <id> already has an active writer` | Rh；本机目录存在 |
| 沙箱警告 | ⚠ 本机的 `configWarning` 变成“PATH 上没有 bubblewrap，用自带的” | 实测 |
| 命令外壳 | 用户 shell 命令包成 `/usr/bin/bash -lc …`（Rh 录的是 zsh）：随 `$SHELL` 变，解析要认 bash/zsh/sh | 实测 |

对话记录：

- `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-<本地时间>-<线程 ID>.jsonl`，归档在 `archived_sessions/`；线程名在 `session_index.jsonl`（每行 `{id, thread_name, updated_at}`，最后一行为准）。实测本机 0.165.0 仍是这样。
- 每行 `{timestamp, type, payload, ordinal}`。第一行 `session_meta`：`id`、`cwd`、`source`、`originator`、`cli_version`。⚠ 0.165 还多了 `session_id`、`thread_source`、`runtime_workspace_roots`、`model_provider`、`base_instructions`、`history_mode`、`context_window`、`creator_*`（实测本机最新一份）。
- 要读的行：`event_msg` 的 `task_started`、`item_completed`、`token_count`、`task_complete`、`turn_aborted`；`turn_context`（模型、思考强度、审批）；`compacted`。条目形状见 Rh §6.4“对话记录”。
- `source` 为 `{subagent: …}` 的是子 agent 线程，归父线程；`exec`（`codex exec`）、`vscode`（app-server 开的）都是顶层。
- ⚠ 新文件 `~/.codex/thread_history_1.sqlite`（本机 163 MB）：表 `thread_turns`、`thread_items`、`thread_history_projection_state`，记着 rollout 的序号和字节偏移，是从 rollout 投影出来的索引，rollout 仍是原件。线程的 `historyMode` 是 `paginated`，app-server 有 `thread/turns/list`、`thread/items/list` 分页读历史。（读的是复制到临时目录的副本。）
- ⚠ 新通知 `thread/queue/changed`、新库 `queue_1.sqlite`：Codex 自己有了待发队列，Iridium 没用到前不碰。

## 5. pi

沿用 Rh §6.3（1.3.0）；1.4.0 CHANGELOG 没有改 rpc 的条目。

| 项 | 事实 | 核实 |
|---|---|---|
| 启动 | `pi --mode rpc --session-id <id> --append-system-prompt <文字或文件> [-e <审批扩展>]`；`--session <路径或 ID>` 不看工作目录 | `docs/cli.md`；Rh |
| 帧 | 严格按 LF 分行；不能用 node `readline`（会在 U+2028/2029 切开）；stdout 只放协议 | `docs/rpc.md` |
| 发消息 | `{"type":"prompt","message":…}`，应答 `data.disposition`：`started`、`queued`、`handled` | `docs/rpc-commands.md`；Rh |
| 插话 | 回合中 `prompt` 加 `"streamingBehavior":"steer"`（或 `followUp`）；也有独立的 `steer`、`follow_up` 命令；`clear_queue` 取回排队的字 | 文档 |
| 中断 | `abort`：等回合停下、发 `agent_settled`（`aborted: true`）再应答 | Rh `abort` |
| 回合结束 | 以 `agent_settled` 为准，`agent_end` 之后还可能重试、压缩、插话 | `docs/rpc.md` |
| 审批 | pi 本身没有。Rh 的扩展 `src/pi-approval.ts`：`tool_call` 钩子里除 `read`、`grep`、`find`、`ls` 外都 `ctx.ui.select(<JSON 标题>, ["allow","deny"], {signal: ctx.signal})`；rpc 上是 `extension_ui_request`，答 `extension_ui_response {id, value: "allow" / "deny: 理由"}`；必须传 `ctx.signal`，否则挂着审批时 `abort` 卡住 | Rh 源码 + `approval-*` 录制 |
| 模型/强度 | `set_model`、`set_thinking_level`（不认识的级别静默变 `off`）、`get_available_models`、`get_available_thinking_levels`；换模型会重设强度 | Rh |
| 压缩 | `compact`（回合中发会先 abort）；事件 `compaction_start {reason: manual/threshold/overflow}`、`compaction_end {reason, result{summary, firstKeptEntryId, tokensBefore, estimatedTokensAfter, usage}, aborted, willRetry, errorMessage?}`；重试 `summarization_retry_*` | `docs/json.md` |
| 用量 | `get_session_stats` → `sessionFile`、`sessionId`、各类消息数、`tokens{input,output,cacheRead,cacheWrite,total}`、`cost`（美元）、`contextUsage{tokens, contextWindow, percent}`；压缩后 `tokens`、`percent` 为 null，直到下一次回复 | 实测 1.4.0（空会话全 0、`contextWindow` 1000000）；文档 |
| 状态 | `get_state`：`model`、`thinkingLevel`、`isStreaming`、`isCompacting`、`sessionFile`、`sessionId`、`messageCount`、`pendingMessageCount`、`steeringMode` | 实测 |
| 会话文件 | 默认 `~/.pi/agent/sessions/--<cwd 去掉开头/，/\: 换成 ->--/<UTC 时间>_<UUIDv7>.jsonl`；`--session-dir`、`PI_CODING_AGENT_SESSION_DIR` 改目录（不分子目录）。第一条消息前不写文件（实测：起进程、跑 rpc `bash` 后目录仍空） | Rh；实测 |
| 文件格式 | 第一行 `{"type":"session","version":3,id,timestamp,cwd,parentSession?}`；其余条目 `id`/`parentId` 成树；`message`（role `system`/`user`/`assistant`/`toolResult`/`bashExecution`/…）、`model_change`、`thinking_level_change`、`compaction`、`usage`、`session_info`（名字）等 | `docs/session-format.md` |
| 本机默认模型 | ⚠ `opencode-go` 的 `claude-haiku-5-5`，强度 `high`（Rh 时是 DeepSeek）。按 AGENTS.md 真跑 pi 前先确认它便宜、不走 Claude 订阅 | 实测 `get_state` |

## 6. dsh（DeepSeek Harness）

读 `deepseek-ai/deepseek-harness` 的 `dsh-v0.2.2-rc.1`（commit `66491b0`），用 `gh api` 取文件，没装、没跑。路径都在仓库里。README 写着“developer preview… THERE WILL BE COMPATIBILITY-BREAKING CHANGES”。

会话记录：

| 项 | 事实 | 出处 |
|---|---|---|
| 谁写 | 插件 `@deepseek-ai/dsh-session-persistence-jsonl`（行 id `session-persistence-jsonl`），sdk、acp、headless、web 都继承 | `packages/bundle/base/cordis.patch.yml` |
| 位置 | `~/.dsh/sessions/--<projectKey(cwd)>--/<会话 ID>/session.v4.jsonl.zstd`；`DSH_HOME` 改根目录，没有命令行参数。`projectKey`：`/ \ :` 换成 `-`（连续的并成一个），去开头的 `-`，截到 251 字，会撞，所以按目录列时要再核会话头的 `cwd`；没有 cwd 的在 `_no-cwd/` | `session-persistence-jsonl/src/format.ts`、`packages/util/home-paths` |
| 格式 | ⚠ 默认 zstd 压缩：会话头一帧，之后每批追加一帧，末帧可能写了一半。Go 要能读连续多帧、容忍断尾（`compression: 'none'` 才是明文 JSONL）。版本 4，旧版文件 `session.jsonl`、`session.v1…` 可能并存，取最大的 `vN` | `format.ts`、`packages/core/session/src/types.ts` |
| 会话头 | `{type:'session', version, id, createdAt(毫秒), cwd?, parentSession?, isSeeded, origin?:'subagent', delegationDepth, agentPreset?}` | `format.ts` |
| 记录行 | `{type, seq, time, data, ignorable?, surfaceOp?, sourceEventSeqs?}`；事件种类表在 `known-event-types.ts` | `types.ts` |
| 子 agent | `origin:'subagent'` 且 `delegationDepth >= 1`；只有 `parentSession` 的可能是分叉，不算 | `types.ts` |
| 会话 ID | headless、web：`session-<uuid>`；TS SDK：`session-<32 位 hex>`；ACP：裸 UUID；SDK 服务端收客户端给的任意字符串 | 子 agent 读源码所得 |
| 标题 | 事件 `session/title {title, source.kind: fallback/provider/user}`，最后一条为准；sdk、acp 关了模型起名 | `packages/session/session-title` |
| 写锁 | `<会话目录>/session.lock` 非阻塞 flock；Iridium 用 `LOCK_EX|LOCK_NB` 试，就知道有没有别的进程在写 | `session-persistence-jsonl/README.md` |
| 中断的会话 | 文件可能没有 `turn/end`，恢复时补一条 `interrupted` | 源码 |

SDK 模式（`dsh --profile sdk`，自定的 JSON-RPC 2.0，每行一条，不是 ACP）：

| 项 | 事实 |
|---|---|
| 握手 | `initialize {cwd, provider, model, reasoningEffort?, maxTokens?, approvalRequests?, capabilities?}`；⚠ 模型整个进程固定，运行中不能换，也没有模型列表 |
| 发消息 | `session/prompt {sessionId, contentBlocks}` → `{messageId}`（只是收下回执）；ID 不存在就新建。`session/query` 同样，另认 `/compact`、`/permission <预设>`、`/plan`、`/goal` |
| 其他请求 | `session/resume`、`session/wait`、`session/working-directory/get|set`、`shutdown` |
| 通知 | `session.event {sessionId, event}`（每条落盘事件）、`session.status {running|idle}`、`session.query.completed`、`subagent.started|finished` |
| 审批 | dsh 发请求 `approval/request {sessionId, toolName, callId?, reason?}`，回 `{outcome: allowed-once|rejected|cancelled}`；还有 `user-question/ask` |
| 权限预设 | `read-only`、`workspace-write`（默认，审批 `ask`）、`danger-full-access`（审批 `never`）；运行中用 `/permission <预设>` 切，按会话、会落盘 |
| 插话 | 没有专门方法；回合中发的 `session/prompt` 进收件箱，下一步取（推断，未确认） |
| 中断 | ⚠ 没有：`sdk/server/README.md` 写明“no per-session close or prompt-cancel method”，只能关进程 |
| 增量 | ⚠ 没有正文增量，`assistant/message` 整条来 |
| 事件 | `turn/start`、`turn/end {reason.kind: completed|aborted|blocked|error|max-tokens|interrupted|forked}`、`step/start|end`、`tool/call {callId, name, arguments}`、`tool/result`、`compaction/start|end` |
| 用量 | `assistant/message.usage {inputTokens, outputTokens, totalTokens?, cacheReadTokens?, cacheWriteTokens?, reasoningTokens?}`；上下文窗口在 `request/context {provider, model, contextWindow}`；没找到费用字段 |

出处：`packages/sdk/protocol/src/types.ts`、`packages/sdk/protocol/README.md`、`packages/sdk/server/README.md`。

ACP 模式（`dsh --profile acp`，标准 ACP v1，`packages/acp/acp`）：有 `session/new|list|resume|close`、`session/cancel`、`session/set_config_option`（`model`、`reasoning_effort`）、`session/request_permission`、`usage_update {used, size}`；也没有正文增量（每条消息一块）、没有模式切换、没有手动压缩，一个会话一次一条 prompt。

其他：

- 追加系统提示：没有参数和协议字段。用 `--patch x.yml` 给 `system-prompt` 行设 `personaSuffix`；patch 整个替换这一行的配置，要连 `personaPrefix` 一起写；`{{` 会被当变量。`AGENTS.md`、`CLAUDE.md` 作为用户消息载入，不算系统提示。出处 `packages/core/system-prompt/src/index.ts`。
- 子进程环境（模型调的 shell）：`DSH_HOME`、`DSH_SHELL=1`、`DSH_SESSION_ID`（调用的会话）、`DSH_PROFILE`、`DSH_PROFILE_DIR`。出处 `packages/shell/shell-env/README.md`（我核过这一条）。
- dsh 读的变量：`DSH_HOME`、`DSH_AGENTS_HOME`、`DSH_PERMISSION_MODE`、`DSH_TELEMETRY_*`（遥测默认开，`FEEDBACK_ONLY`）、`DEEPSEEK_API_KEY`、`DEEPSEEK_BASE_URL`。
- 登录：key 名 `DEEPSEEK_API_KEY`，按顺序找 `$DSH_HOME/.credentials.yaml`、环境、项目 `.env`、用户 `.env`。默认 `https://api.deepseek.com/anthropic`，模型 `deepseek-official/deepseek-flash`，窗口 1,048,576，强度 `off`、`low`、`high`、`max`。出处 `packages/llm/llm-deepseek*`、`packages/credentials/credentials-local`。
- 进程：stdin 关闭后正常退出，5 秒宽限；SIGTERM 退 0。回合中关会取消回合，已落盘的留着（推断）。进程标题没改（未确认），命令行能看到 `dsh` 和 profile。

## 7. cloudflared 2026.10.0

| 项 | 事实 | 核实 |
|---|---|---|
| `tunnel login` | 开浏览器登录、选一个站点（zone），写 `~/.cloudflared/cert.pem`。内容含 `zoneID`、`accountID`、`apiToken` | `--help`；文档 create-local-tunnel；源码 `credentials/origin_cert.go` |
| 能管什么 | 账户内所有隧道（建、删、列）。DNS 路由只发到 cert 里那个 zone：`PUT /zones/<zoneID>/tunnels/<id>/routes`。别的 zone 要再登录一次，用 `--origincert` 指另一份 cert | 文档 tunnel-permissions（“Account-wide”）；源码 `cfapi/base_client.go`、`cfapi/hostname.go` |
| `tunnel create <名>` | 在账户里建隧道，写 `~/.cloudflared/<UUID>.json`（`--credentials-file` 可改，`-o json` 输出）；这个文件只能跑这一条隧道 | `--help`；文档 |
| `tunnel route dns <隧道> <主机名>` | 建 CNAME 到 `<UUID>.cfargotunnel.com`。已有 A/AAAA/CNAME 时失败（API 错误 1003 “record with that host already exists”）；`--overwrite-dns`/`-f`（或 `TUNNEL_FORCE_PROVISIONING_DNS`）覆盖。已指向这条隧道时回 “already configured”，不算错 | `--help`；源码 `SuccessSummary`；错误码来自网上 issue 和故障文档，未实测 |
| 跑隧道 | `cloudflared tunnel --config <文件> run [隧道]`；配置 `tunnel: <UUID>`、`credentials-file: <路径>`、`ingress:`（最后一条要兜底，如 `service: http_status:404`） | `--help`；文档 |
| 用户服务 | `cloudflared service install` 只装系统服务（要 root），没有用户版。Iridium 自己写 `systemd --user` 单元跑上面的命令；本机系统 cloudflared 和 `/etc/cloudflared` 不碰 | `service --help`；文档 |
| 删 DNS 记录 | cloudflared 没有删记录的命令（`route dns` 只建/覆盖）；`tunnel delete` 不删 CNAME。删记录要用控制台或带 DNS 权限的 API 令牌。cert 里的 `apiToken` 能不能删记录，文档没说，不靠它 | `tunnel --help`、`route --help`；文档 |
| 请求体上限 | 按站点套餐：Free/Pro 100 MB，Business 200 MB，Enterprise 默认最多 5 GB。计划里 50 MB 一块够用 | 文档 workers/platform/limits |

## 8. 对计划的影响

- 拉起者：Claude、Codex 的子进程环境里有会话 ID，读 `/proc/<pid>/environ` 就能定父会话；pi 只有 `AI_AGENT=pi`（模型的 bash 工具才带 `PI_SESSION_ID`）；dsh 的模型 shell 带 `DSH_SESSION_ID`。
- dsh 托管：SDK 模式不能中断、不能换模型、没有正文增量。要中断和换模型就得用 ACP 模式（有 `session/cancel`、`set_config_option`），但 ACP 没有权限预设切换和手动压缩。两种都不能满足“全功能”，先定用哪个。
- dsh 历史默认是 zstd 多帧文件，Go 端要纯 Go 的 zstd 解码（如 `klauspost/compress`）。
- Claude 的 Agent 子任务没有自己的会话 ID，命令里是父会话的，正好“算在原会话里”。
- Claude 权限模式改名：网页列 `manual`（旧名 `default` 仍收）、`auto`、`dontAsk` 等，按 `--help` 的列表。
- Codex 0.165：历史另有 SQLite 投影和分页接口，记录文件仍可读；`granular` 审批、`account/usage/read` 是新的，先不用。
- pi 本机默认模型变了，真跑前先选便宜的。
- Cloudflare：一份 cert 只管一个 zone 的 DNS；删记录需要 API 令牌或控制台，Iridium 的设置指引里要写明。
