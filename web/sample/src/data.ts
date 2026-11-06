// Fake data for the style sample. Text that would be the user's own data comes in both languages here
// only so the English screenshots can be judged at English lengths.
import type { Lang } from "./i18n";

export type Agent = "claude" | "codex" | "pi" | "dsh";
export type Status = "running" | "compacting" | "needs" | "error" | "starting" | "elsewhere" | "idle";
type L = { zh: string; en: string };
export const pick = (l: L | string, lang: Lang) => (typeof l === "string" ? l : l[lang]);

export type Session = { id: string; agent: Agent; title: L | string; status: Status; unread?: boolean };
export type Folder = { id: string; name: string; sessions: Session[] };
export type Workspace = { id: string; name: L | string; root: string; sessions: Session[]; folders: Folder[] };

const s = (id: string, agent: Agent, title: L | string, status: Status = "idle", unread = false): Session => ({ id, agent, title, status, unread });

export const workspaces: Workspace[] = [
  {
    id: "w-ir",
    name: "Iridium",
    root: "~/kumo/Iridium",
    sessions: [
      s("s-holder", "codex", { zh: "Holder：命名管道测试", en: "Holder: named pipe tests" }, "running"),
      s("s-sample", "claude", { zh: "样张：侧栏和对话页", en: "Style sample: sidebar and chat" }, "needs"),
      s("s-disc", "pi", { zh: "从 ~/.claude/sessions 发现会话", en: "Discover sessions from ~/.claude/sessions" }, "idle", true),
      s("s-plan", "claude", { zh: "计划审阅", en: "Plan review" }, "elsewhere"),
    ],
    folders: [
      {
        id: "f-web",
        name: "web",
        sessions: [
          s("s-size", "dsh", { zh: "首屏体积预算", en: "First-screen size budget" }, "compacting"),
          s("s-pop", "claude", { zh: "Base UI 弹出框动效", en: "Base UI popover motion" }),
        ],
      },
    ],
  },
  {
    id: "w-kdj",
    name: "KDJ",
    root: "~/kumo/KDJ",
    sessions: [
      s("s-mini", "claude", { zh: "迷你播放器从角落弹出", en: "Mini player pops out of the corner" }, "error"),
      s("s-proxy", "codex", { zh: "Windows 代理 os error 10054", en: "Windows proxy os error 10054" }, "starting"),
    ],
    folders: [
      {
        id: "f-tauri",
        name: "src-tauri",
        sessions: [
          s("s-mpv", "codex", "libmpv video window"),
          s("s-cookie", "claude", { zh: "Cookie 续期", en: "Cookie upkeep" }, "idle", true),
          s("s-bpm", "pi", "SoundCloud BPM"),
          s("s-rustls", "codex", "rustls advisories"),
        ],
      },
    ],
  },
  {
    id: "w-rh",
    name: "Rhodium",
    root: "~/kumo/rhodium-src/Rhodium",
    sessions: [s("s-rh1", "claude", { zh: "网关限速", en: "Gateway rate limits" }), s("s-rh2", "codex", "holder restart")],
    folders: [],
  },
  {
    id: "w-none",
    name: { zh: "未归类", en: "Unsorted" },
    root: "~",
    sessions: [s("s-scratch", "pi", "scratch")],
    folders: [],
  },
];

export const terminals = {
  me: { name: "kumo-m2-mac", avatar: "/fake/avatar-me.svg" },
  phone: { name: "kumo-iphone", avatar: "/fake/avatar-phone.svg" },
  bot: { name: "qq-bot", avatar: "/fake/avatar-bot.svg" },
};

export type Block =
  | { t: "text"; text: L }
  | { t: "tool"; name: string; arg: string; stat?: string }
  | { t: "image"; src: string; name: string }
  | { t: "file"; name: string; size: string };

export type Message =
  | { kind: "system"; text: L; time: string }
  | { kind: "you"; id: string; text: L | string; panel?: keyof typeof terminals }
  | { kind: "letter"; via: "ir" | "plugin" | "peer"; from: { name: L | string; agent?: Agent; sessionId?: string; avatar?: string }; text: L }
  | { kind: "agent"; id: string; blocks: Block[] };

export const chat: Message[] = [
  { kind: "system", text: { zh: "已从中断处恢复", en: "Resumed where it stopped" }, time: "18:02" },
  {
    kind: "you",
    id: "m1",
    text: {
      zh: "侧栏照 KDJ 的样子做：没有底色、留白分组、选中只加粗。",
      en: "Make the sidebar look like KDJ's: no background, groups split by whitespace, selected is just bold.",
    },
  },
  {
    kind: "agent",
    id: "m2",
    blocks: [
      { t: "tool", name: "Read", arg: "docs/plan.md" },
      { t: "tool", name: "Edit", arg: "web/src/Sidebar.tsx", stat: "+48 −12" },
      {
        t: "text",
        text: {
          zh: "改好了。箭头单独一列，目录只有一个会话时不显示这一层；状态灯放在 logo 右下角，颜色只给 logo 和灯。截图和改动的样式都在下面。",
          en: "Done. The chevrons have their own column, and a folder with a single session is not shown; the lamps sit on the logo's corner, and only logos and lamps carry colour. The screenshot and the changed styles are below.",
        },
      },
      { t: "image", src: "/fake/sidebar-preview.svg", name: "sidebar-preview.png" },
      { t: "file", name: "sidebar-tokens.css", size: "4.2 KB" },
    ],
  },
  {
    kind: "letter",
    via: "ir",
    from: { name: { zh: "Holder：命名管道测试", en: "Holder: named pipe tests" }, agent: "codex", sessionId: "s-holder" },
    text: {
      zh: "测试全绿了，命名管道的 EOF 也处理好了。要不要顺手把 hold 的超时改成 30 秒？",
      en: "Tests are green and the named pipe's EOF is handled. Shall I also raise hold's timeout to 30 seconds?",
    },
  },
  {
    kind: "you",
    id: "m3",
    panel: "phone",
    text: { zh: "先别改超时，等我看完截图。", en: "Leave the timeout for now; I'll look at the screenshots first." },
  },
  {
    kind: "letter",
    via: "plugin",
    from: { name: "QQ·张三", avatar: "/fake/avatar-bot.svg" },
    text: { zh: "今晚能把截图发我看看吗？", en: "Could you send me the screenshots tonight?" },
  },
  {
    kind: "agent",
    id: "m4",
    blocks: [
      {
        t: "text",
        text: {
          zh: "好。先重新构建一遍，再把截图放到 `web/sample/shots/`。",
          en: "Sure. I'll rebuild first, then put the screenshots in `web/sample/shots/`.",
        },
      },
    ],
  },
];

export const approval = {
  tool: "Bash",
  command: "rm -rf web/dist && npm run build",
  cwd: "~/kumo/Iridium",
  why: { zh: "清掉旧的构建产物再重新构建，截图前确认体积。", en: "Clear the old build output and rebuild, to check the size before taking screenshots." },
};

export const reply: L = {
  zh: "收到，截图之前不动超时。",
  en: "Got it, the timeout stays as it is until after the screenshots.",
};

export type QuotaKey = "c5h" | "c7d" | "c7dFable";
export const quotas: { key: QuotaKey; pct: number; reset: L }[] = [
  { key: "c5h", pct: 62, reset: { zh: "今天 21:40", en: "today 21:40" } },
  { key: "c7d", pct: 86, reset: { zh: "周一 09:00", en: "Mon 09:00" } },
  { key: "c7dFable", pct: 31, reset: { zh: "周一 09:00", en: "Mon 09:00" } },
];

export type CtxKey = "system" | "tools" | "mcp" | "memory" | "messages" | "reserve" | "free";
export const context: { total: number; parts: { key: CtxKey; k: number }[] } = {
  total: 200,
  parts: [
    { key: "system", k: 3.1 },
    { key: "tools", k: 14.2 },
    { key: "mcp", k: 6.8 },
    { key: "memory", k: 4.4 },
    { key: "messages", k: 55.5 },
    { key: "reserve", k: 33 },
    { key: "free", k: 83 },
  ],
};

export const modes = ["default", "acceptEdits", "plan", "bypass"] as const;
export type Mode = (typeof modes)[number];

/** A small fake file tree for the folder picker: folders only. */
const tree: Record<string, string[]> = {
  "~": ["Downloads", "kumo", "notes", "src"],
  "~/kumo": ["blog", "dotfiles", "Iridium", "KDJ", "rhodium-src", "winsov8"],
  "~/kumo/Iridium": ["cmd", "docs", "internal", "scripts", "web"],
  "~/kumo/Iridium/web": ["sample", "src"],
  "~/kumo/KDJ": ["crates", "docs", "scripts", "src", "src-tauri"],
};
export const listFolders = (path: string) => tree[path] ?? [];
