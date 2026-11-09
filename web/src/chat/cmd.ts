// What a shell call does, by its command: the first command in it that means something, past
// cd, variables, sudo and env; its description when that says nothing. Picks the call's icon.

/** A shell call's sort; none for one that says nothing we know. */
export type Cmd =
  | "ir-send"
  | "ir-archive"
  | "ir-unarchive"
  | "ir-new"
  | "ir-wait"
  | "ir-read"
  | "ir-list"
  | "ir-find"
  | "ir-stop"
  | "ir-files"
  | "ir-access"
  | "ir-profile"
  | "ir-rename"
  | "ir-ws"
  | "ir-machines"
  | "ir-secret"
  | "ir-usage"
  | "ir-bot"
  | "git"
  | "git-commit"
  | "git-push"
  | "git-pull"
  | "git-diff"
  | "git-log"
  | "git-status"
  | "git-branch"
  | "gh"
  | "test"
  | "build"
  | "install"
  | "net"
  | "remote"
  | "search"
  | "read"
  | "list"
  | "file"
  | "delete"
  | "script"
  | "service"
  | "wait"
  | "stop";

/** The shell tools: Claude's Bash, pi's bash, Codex's exec_command and shell. */
const SHELLS = new Set(["bash", "exec_command", "shell", "local_shell"]);

/** A call's sort, if it runs a shell command. */
export function cmdOfCall(t: { name?: string; input?: string }): Cmd | undefined {
  const i = shellInput(t);
  return i && cmdOf(commandOf(i), typeof i.description === "string" ? i.description : undefined);
}

/** Whether a call is only an agent naming its session (ir rename): the chat leaves it out. */
export function renameOnly(t: { name?: string; input?: string }): boolean {
  const i = shellInput(t);
  return !!i && renames(commandOf(i));
}

function renames(command: string): boolean {
  const segs = segments(command);
  const w = segs.length === 1 && !segs[0]!.out ? strip(segs[0]!.words) : undefined;
  if (!w) return false;
  const sh = shellArg(w);
  return sh !== undefined ? renames(sh) : /^(ir|iridium)$/.test(name(w[0]!)) && w[1] === "rename";
}

const commandOf = (i: Record<string, unknown>) => {
  const c = i.command ?? i.cmd;
  return typeof c === "string" ? c : Array.isArray(c) ? (shellArg(c.map(String)) ?? c.map(String).join(" ")) : "";
};

/** A shell call's input; none for another tool's. */
function shellInput(t: { name?: string; input?: string }): Record<string, unknown> | undefined {
  if (!SHELLS.has((t.name ?? "").toLowerCase())) return undefined;
  let i: Record<string, unknown> = {};
  try {
    const v = JSON.parse(t.input ?? "");
    if (v && typeof v === "object") i = v;
  } catch {
    // An input cut short: its command and description as far as they go.
    for (const m of (t.input ?? "").matchAll(/"(command|cmd|description)"\s*:\s*"((?:[^"\\]|\\.)*)/g)) i[m[1]!] ??= m[2]!.replace(/\\n/g, "\n").replace(/\\(.)/g, "$1");
  }
  return i;
}

/** A command's sort: its first command that means something decides; one we don't know falls to
 *  what the description says. */
export function cmdOf(command: string, description?: string): Cmd | undefined {
  const segs = segments(command);
  for (let n = 0; n < segs.length; n++) {
    const words = strip(segs[n]!.words);
    if (!words) continue;
    const base = name(words[0]!);
    if (SKIP.has(base) || base.startsWith("#")) continue;
    if ((base === "echo" || base === "printf") && !segs[n]!.out && n < segs.length - 1) continue;
    const sh = shellArg(words);
    if (sh !== undefined) return cmdOf(sh, description);
    return sortOf(base, words.slice(1), segs[n]!.out) ?? said(description);
  }
  return said(description);
}

/** What `bash -c "..."` and its kin run. */
function shellArg(words: string[]): string | undefined {
  if (!/^(ba|z|da|k|fi)?sh$/.test(name(words[0] ?? ""))) return undefined;
  const c = words.findIndex((w, i) => i > 0 && /^-[a-z]*c[a-z]*$/.test(w));
  return c > 0 ? words[c + 1] : undefined;
}

/** Commands that set the scene, not the act. */
const SKIP = new Set(["cd", "pushd", "popd", "export", "unset", "set", "source", ".", "true", ":", "alias", "shopt", "ulimit", "umask", "trap", "[", "[[", "test", "local", "declare", "readonly"]);
/** Words before a command that run it as it is. */
const WRAP = new Set(["time", "nohup", "exec", "command", "builtin", "nice", "ionice", "stdbuf", "caffeinate", "then", "else", "do", "while", "until", "if", "elif", "!", "{", "(", "xargs"]);

const name = (w: string) => w.slice(w.lastIndexOf("/") + 1);

/** The command and its arguments, past variables, sudo, env and the like; none for a segment that
 *  runs nothing (a loop's head, only variables). */
function strip(ws: string[]): string[] | undefined {
  let i = 0;
  const flags = (takes: string[] = []) => {
    while (i < ws.length && ws[i]!.startsWith("-")) i += takes.includes(ws[i]!) ? 2 : 1;
  };
  while (i < ws.length) {
    const w = ws[i]!;
    const b = name(w);
    if (/^[A-Za-z_][A-Za-z0-9_]*=/.test(w)) i++;
    else if (b === "sudo" || b === "doas") (i++, flags(["-u", "-g", "-C", "-D", "-h", "-p", "-r", "-t", "-U"]));
    else if (b === "env") {
      i++;
      flags(["-u", "-C", "-S", "--unset", "--chdir"]);
    } else if (b === "timeout") {
      i++;
      flags(["-s", "-k", "--signal", "--kill-after"]);
      i++; // the duration
    } else if (WRAP.has(b)) (i++, flags(["-n", "-o", "-e", "-i", "-I", "-L", "-P", "-c"]));
    else if (b === "for" || b === "case" || b === "select" || b === "done" || b === "fi" || b === "esac" || b === "}" || b === ")") return undefined;
    else break;
  }
  return i < ws.length ? ws.slice(i) : undefined;
}

/** A known command's sort by its name, its arguments, and whether it writes out to a file. */
function sortOf(cmd: string, args: string[], out: boolean): Cmd | undefined {
  const pos = args.filter((a) => !a.startsWith("-"));
  const sub = pos[0];
  switch (cmd) {
    case "ir":
    case "iridium":
      return IR[sub ?? ""];
    case "git":
      return gitOf(args);
    case "gh":
      return "gh";
    case "go":
      return sub === "test" || sub === "vet" ? "test" : sub === "build" || sub === "install" || sub === "generate" ? "build" : sub === "get" || sub === "mod" ? "install" : sub === "run" ? "script" : undefined;
    case "npm":
    case "pnpm":
    case "yarn":
    case "bun":
      return pkgOf(cmd, pos);
    case "npx":
    case "bunx":
    case "pnpx":
      return pos.length ? (sortOf(name(pos[0]!), args.slice(args.indexOf(pos[0]!) + 1), out) ?? "script") : undefined;
    case "cargo":
      return sub === "test" || sub === "clippy" || sub === "check" ? "test" : sub === "build" ? "build" : sub === "add" || sub === "install" ? "install" : sub === "run" ? "script" : undefined;
    case "make":
      return pos.some((p) => /test|check|lint/.test(p)) ? "test" : pos.includes("install") ? "install" : "build";
    case "node":
      return args.includes("--test") ? "test" : "script";
    case "sed":
      return args.some((a) => /^-[a-zA-Z]*i/.test(a) || a.startsWith("--in-place")) ? "file" : "read";
    case "cat":
    case "echo":
    case "printf":
      return out ? "file" : cmd === "cat" ? "read" : undefined;
  }
  return NAMES[cmd];
}

const IR: Record<string, Cmd> = {
  send: "ir-send",
  archive: "ir-archive",
  unarchive: "ir-unarchive",
  new: "ir-new",
  wait: "ir-wait",
  read: "ir-read",
  ls: "ir-list",
  find: "ir-find",
  stop: "ir-stop",
  cp: "ir-files",
  files: "ir-files",
  invite: "ir-access",
  token: "ir-access",
  access: "ir-access",
  revoke: "ir-access",
  profile: "ir-profile",
  rename: "ir-rename",
  ws: "ir-ws",
  machines: "ir-machines",
  sh: "remote",
  join: "remote",
  pair: "ir-access",
  secret: "ir-secret",
  run: "ir-secret",
  usage: "ir-usage",
  bot: "ir-bot",
  public: "net",
  update: "install",
};

/** git's sort by its command, past its own options (-C repo, -c k=v, --no-pager…). */
function gitOf(args: string[]): Cmd {
  let i = 0;
  while (i < args.length && args[i]!.startsWith("-")) i += ["-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path"].includes(args[i]!) ? 2 : 1;
  const sub = args[i];
  switch (sub) {
    case "commit":
    case "merge":
    case "rebase":
    case "cherry-pick":
    case "revert":
    case "tag":
      return "git-commit";
    case "push":
      return "git-push";
    case "pull":
    case "fetch":
    case "clone":
      return "git-pull";
    case "diff":
    case "show":
      return "git-diff";
    case "log":
    case "reflog":
    case "blame":
    case "shortlog":
      return "git-log";
    case "status":
      return "git-status";
    case "branch":
    case "checkout":
    case "switch":
    case "worktree":
      return "git-branch";
    case "grep":
      return "search";
    default:
      return "git";
  }
}

/** npm and its kin: test, build, install, or a script run. */
function pkgOf(cmd: string, pos: string[]): Cmd | undefined {
  const run = pos.findIndex((p) => p === "run" || p === "run-script");
  const sub = run >= 0 ? pos[run + 1] : pos[0];
  if (run < 0 && cmd === "bun" && sub && /\.[cm]?[jt]sx?$/.test(sub)) return "script";
  if (!sub) return cmd === "yarn" ? "install" : undefined;
  if (/^(t|test|tst)$|test|check|lint|vitest|typecheck/.test(sub)) return "test";
  if (/build|compile|bundle/.test(sub)) return "build";
  if (/^(i|install|ci|add|remove|rm|uninstall|update|up|upgrade)$/.test(sub)) return "install";
  if (/^(x|exec|dlx)$/.test(sub)) return "script";
  return run >= 0 || sub === "start" || sub === "dev" ? "script" : undefined;
}

const NAMES: Record<string, Cmd> = {
  vitest: "test",
  jest: "test",
  pytest: "test",
  mocha: "test",
  playwright: "test",
  golangci_lint: "test",
  "golangci-lint": "test",
  eslint: "test",
  tsc: "build",
  vite: "build",
  esbuild: "build",
  pip: "install",
  pip3: "install",
  uv: "install",
  apt: "install",
  "apt-get": "install",
  brew: "install",
  curl: "net",
  wget: "net",
  http: "net",
  xh: "net",
  ping: "net",
  ssh: "remote",
  scp: "remote",
  rsync: "remote",
  sftp: "remote",
  mosh: "remote",
  grep: "search",
  egrep: "search",
  rg: "search",
  ag: "search",
  ack: "search",
  find: "search",
  fd: "search",
  locate: "search",
  which: "search",
  head: "read",
  tail: "read",
  less: "read",
  more: "read",
  bat: "read",
  wc: "read",
  jq: "read",
  stat: "read",
  file: "read",
  diff: "read",
  ls: "list",
  tree: "list",
  du: "list",
  rm: "delete",
  rmdir: "delete",
  mv: "file",
  cp: "file",
  mkdir: "file",
  touch: "file",
  ln: "file",
  chmod: "file",
  chown: "file",
  tee: "file",
  tar: "file",
  unzip: "file",
  python: "script",
  python3: "script",
  deno: "script",
  tsx: "script",
  "ts-node": "script",
  ruby: "script",
  perl: "script",
  lua: "script",
  systemctl: "service",
  journalctl: "service",
  service: "service",
  launchctl: "service",
  docker: "service",
  "docker-compose": "service",
  podman: "service",
  kubectl: "service",
  sleep: "wait",
  kill: "stop",
  pkill: "stop",
  killall: "stop",
};

/** A description's sort, by its words, for a command we don't know. */
function said(d?: string): Cmd | undefined {
  if (!d) return undefined;
  for (const [re, c] of SAID) if (re.test(d)) return c;
  return undefined;
}
const SAID: [RegExp, Cmd][] = [
  [/\btests?\b|测试/i, "test"],
  [/\b(build|compile)\b|构建|编译/i, "build"],
  [/\binstall\b|安装/i, "install"],
  [/\b(search|find|grep)\b|搜索|查找/i, "search"],
  [/\b(fetch|download|request)\b|下载|请求/i, "net"],
  [/\blist\b|列出/i, "list"],
  [/\b(read|show|view|print|inspect)\b|查看|读取/i, "read"],
];

/** A command's pieces between &&, ||, ;, |, & and line ends, each as its words with quotes taken
 *  off; out: it writes to a file with > (not to /dev/null or another stream). */
function segments(s: string): { words: string[]; out: boolean }[] {
  const segs: { words: string[]; out: boolean }[] = [];
  let words: string[] = [];
  let out = false;
  let w = "";
  let has = false; // w is a word, maybe an empty one ("")
  let redirect = 0; // 1: the next word is where > goes; 2: where < or << reads from
  const word = () => {
    if (has) {
      if (redirect === 1 && w !== "/dev/null") out = true;
      if (redirect) redirect = 0;
      else words.push(w);
    }
    w = "";
    has = false;
  };
  const end = () => {
    word();
    if (words.length) segs.push({ words, out });
    words = [];
    out = false;
  };
  for (let i = 0; i < s.length; i++) {
    const c = s[i]!;
    if (c === "\\" && i + 1 < s.length) {
      if (s[i + 1] !== "\n") w += s[i + 1];
      has = s[i + 1] !== "\n" || has;
      i++;
    } else if (c === "'") {
      const j = s.indexOf("'", i + 1);
      w += s.slice(i + 1, j < 0 ? undefined : j);
      has = true;
      i = j < 0 ? s.length : j;
    } else if (c === '"') {
      let j = i + 1;
      for (; j < s.length && s[j] !== '"'; j++) {
        if (s[j] === "\\" && j + 1 < s.length) j++;
        w += s[j];
      }
      has = true;
      i = j;
    } else if ((c === "$" && s[i + 1] === "(") || c === "`") {
      // A command run for its output stays one piece of its word.
      let j = c === "`" ? s.indexOf("`", i + 1) : i + 1;
      if (c === "$") {
        let depth = 0;
        for (; j < s.length; j++) {
          if (s[j] === "(") depth++;
          else if (s[j] === ")" && --depth === 0) break;
        }
      }
      if (j < 0 || j >= s.length) j = s.length - 1;
      w += s.slice(i, j + 1);
      has = true;
      i = j;
    } else if (c === " " || c === "\t") word();
    else if (c === "\n" || c === ";" || c === "|" || c === "&") {
      if (c === "&" && (s[i + 1] === ">" || s[i - 1] === ">")) {
        word();
        if (s[i + 1] === ">") (redirect = 2, i++); // &> file: output and errors both
        continue;
      }
      end();
      if ((c === "&" || c === "|") && s[i + 1] === c) i++;
    } else if (c === ">" || c === "<") {
      const fd = !has || /^\d$/.test(w) ? w : undefined;
      if (fd === undefined) word();
      else ((w = ""), (has = false));
      while (s[i + 1] === c || s[i + 1] === "|") i++;
      if (s[i + 1] === "&") {
        i++; // >&2, 2>&1: to another stream
        while (/\d|-/.test(s[i + 1] ?? "")) i++;
      } else redirect = c === ">" && fd !== "2" ? 1 : 2;
    } else if ((c === "(" || c === ")" || c === "{" || c === "}") && !has) {
      end();
    } else {
      w += c;
      has = true;
    }
  }
  end();
  return segs;
}
