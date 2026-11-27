// What 管理 (manage.tsx) takes of the old 应用管理: adding a computer (its join line), a computer's
// folders, a login's passkeys, renaming in place, removing, and the helpers they share. 配对's
// requests and a grant's wait as cards (confirms.tsx). No TOKEN is handed out here.
import { Menu } from "@base-ui/react/menu";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useQuery } from "./api/query";
import {
  Check,
  ChevronLeft,
  EyeOff,
  FolderOpen,
  Laptop,
  Link2,
  LoaderCircle,
  Monitor,
  MonitorSmartphone,
  Plus,
  Smartphone,
  Trash2,
  type LucideIcon } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError, api } from "./api/client";
import type {
  App,
  Computer,
  Device,
  Login,
  MachineGrant,
  Passkey,
  SetOwner,
  PasskeyLink } from "./api/types.gen";
import { CardFrame, CardHead, MainKey, SecondKey } from "./card";
import { t, useLang } from "./i18n";
import { Code } from "./chat/hl";
import { Segmented } from "./me";
import { Crop } from "./profile";
import { SecretHere } from "./secret";
import { showSecret } from "./secrets";
import { Group, Item } from "./rows";
import { errText, toast, toasts } from "./toast";
import { agentName, AGENTS as AGENT_MARKS, ago, CopyKey, IconKey, Logo, own, Switch, Tip, useLongPress, useMenuAt, usePointAnchor, intoView, SureKey, MenuPortal } from "./ui";
import { MenuItems, type MenuItem } from "./sidebar";

export const systems: Record<string, string> = {
  windows: "Windows",
  darwin: "macOS",
  linux: "Linux",
};


/** A machine's line: its system and agents, e.g. "Linux · claude 2.1 · codex". */
/** A computer's agents, those not turned off, that have a mark. */
export const marked = (m: Computer) =>
  m.agents.filter((a) => !a.off && (AGENT_MARKS as string[]).includes(a.agent));

export const clock = (ms: number) =>
  new Date(ms).toLocaleTimeString(document.documentElement.lang || undefined, {
    hour: "2-digit",
    minute: "2-digit",
  });

export const nameError = (e: unknown) => {
  const code =
    e instanceof ApiError
      ? (e.body as { nameError?: string } | undefined)?.nameError
      : undefined;
  return code === "taken"
    ? t("me.nameTaken")
    : code
      ? t("me.nameBad")
      : errText(e);
};

/** A login of ir_web (its logins come with app "" from before 0.2's other apps, or "ir_web"). */
export const ofWeb = (l: Login) => !l.app || l.app === "ir_web";

/** A computer's system, as its join line is written: macOS and Linux one (sh), Windows another. */
export type JoinOS = "unix" | "win";

/** What the computer is asked (ir's options, docs/exec.md "安装"): kept running as its service
 *  (ir start, else ir serve in that terminal), command lines, only some agents (none given: all),
 *  only some folders ("": all), the downloads through this Iridium (proxy) and not GitHub. */
export type JoinTerms = {
  keep?: boolean;
  shell?: boolean;
  agents?: string[];
  roots?: string;
  proxy?: boolean;
};

/** ir's words for joining at: an executor only (--no-server), with the terms. */
export function joinArgs(at: string, j: JoinTerms): string[] {
  const a = [j.keep ? "start" : "serve", "--no-server", "--join", at];
  if (j.shell === false) a.push("--no-shell");
  if (j.agents?.length) a.push("--agents", j.agents.join(","));
  const roots = j.roots?.trim();
  if (roots) a.push("--roots", roots);
  if (j.proxy) a.push("--proxy");
  return a;
}

/** A word of sh or PowerShell: as it is when it is plain, else in single quotes. */
function word(os: JoinOS, w: string): string {
  if (/^[A-Za-z0-9_@%+=:,./~-]+$/.test(w)) return w;
  return os === "win"
    ? `'${w.replace(/'/g, "''")}'`
    : `'${w.replace(/'/g, `'\\''`)}'`;
}

/** Where the install script is (internal/joinscript): GitHub's latest release (recommended), or
 *  this Iridium's, whose downloads come through it (proxy). */
export const INSTALL_SH =
  "https://github.com/kumoSleeping/Iridium/releases/latest/download/install.sh";
export const INSTALL_PS1 =
  "https://github.com/kumoSleeping/Iridium/releases/latest/download/install.ps1";

/** The line for a computer without ir: the install script, then ir with args. Windows: from cmd or
 *  PowerShell alike. */
export function installLine(
  os: JoinOS,
  at: string,
  args: string[],
  proxy = false,
): string {
  const url =
    os === "win"
      ? proxy
        ? `'${at}/install.ps1?proxy'`
        : INSTALL_PS1
      : proxy
        ? `'${at}/install?proxy'`
        : INSTALL_SH;
  const rest = args.map((a) => word(os, a)).join(" ");
  return os === "win"
    ? `powershell -c "& ([scriptblock]::Create((irm ${url}))) ${rest}"`
    : `curl -fsSL ${url} | sh -s -- ${rest}`;
}

/** The line for a computer that has ir: kept running, ir join (to its service); else ir serve. */
export function irLine(os: JoinOS, at: string, j: JoinTerms): string {
  const a = joinArgs(at, j);
  const words = j.keep ? ["join", ...a.slice(3)] : a;
  return ["ir", ...words].map((w) => word(os, w)).join(" ");
}

/** The 电脑 group's +, a page: the lines to run on a computer, for its system, and what they ask of
 *  that computer, one switch a row (docs/前端设计规范.md "设置"): kept running, command lines, each
 *  agent, the folders, downloads through this Iridium. Under them two lines, each with its copy key
 *  in its title's row, one line high, scrolled sideways (to its end at first): for a computer
 *  without ir (the install script, then ir) and for one with it. */
export function AddComputer({ onBack }: { onBack: () => void }) {
  const [os, setOs] = useState<JoinOS>(
    /Win/.test(navigator.platform) ? "win" : "unix",
  );
  const [keep, setKeep] = useState(true);
  const [shell, setShell] = useState(true);
  const [proxy, setProxy] = useState(false);
  const [off, setOff] = useState<string[]>([]);
  const [roots, setRoots] = useState("");
  const at = location.origin;
  const system = (v: JoinOS, label: string) => ({
    v,
    label,
    icon: <span className="px-1 text-xs">{label}</span>,
  });
  const on = AGENT_MARKS.filter((a) => !off.includes(a));
  const terms: JoinTerms = {
    keep,
    shell,
    agents: off.length ? on : undefined,
    roots,
    proxy,
  };
  const line = installLine(os, at, joinArgs(at, terms), proxy);
  const has = irLine(os, at, terms);
  return (
    <Page
      title={t("apps.addComputer")}
      onBack={onBack}
      backLabel={t("settings.terminals")}
    >
      <Segmented
        label={t("apps.system")}
        value={os}
        onChange={(v) => setOs(v as JoinOS)}
        items={[system("unix", "macOS / Linux"), system("win", "Windows")]}
      />
      <div className="flex flex-col">
        <Item label={t("apps.joinKeep")}>
          <Switch label={t("apps.joinKeep")} on={keep} onChange={setKeep} />
        </Item>
        <Item label={t("apps.joinShell")}>
          <Switch label={t("apps.joinShell")} on={shell} onChange={setShell} />
        </Item>
        {AGENT_MARKS.map((a) => (
          <Item
            key={a}
            label={
              <span className="flex items-center gap-2">
                <Logo agent={a} size={14} />
                {agentName(a)}
              </span>
            }
          >
            <Switch
              label={agentName(a)}
              on={!off.includes(a)}
              // One stays on: none would be every one.
              disabled={on.length === 1 && !off.includes(a)}
              onChange={(v) =>
                setOff((o) => (v ? o.filter((x) => x !== a) : [...o, a]))
              }
            />
          </Item>
        ))}
        <Item label={t("apps.joinFolders")}>
          <input
            value={roots}
            onChange={(e) => setRoots(e.target.value)}
            placeholder={t("apps.joinFoldersAll")}
            spellCheck={false}
            autoCapitalize="off"
            className="no-ring h-8 w-48 min-w-0 rounded-md bg-fill px-2.5 font-mono text-xs placeholder:font-sans placeholder:text-ink-3"
          />
        </Item>
        <Item label={t("apps.joinProxy")}>
          <Switch label={t("apps.joinProxy")} on={proxy} onChange={setProxy} />
        </Item>
      </div>
      <Group title={t("apps.joinLine")} action={<CopyKey text={line} />}>
        <Line text={line} lang={os === "win" ? "powershell" : "sh"} />
      </Group>
      <Group title={t("apps.joinHasIr")} action={<CopyKey text={has} />}>
        <Line text={has} lang={os === "win" ? "powershell" : "sh"} />
      </Group>
    </Page>
  );
}

/** A command on one line, scrolled to its end: where the lines differ (the flags, the address). */
function Line({ text, lang }: { text: string; lang: string }) {
  const box = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (box.current) box.current.scrollLeft = box.current.scrollWidth;
  }, [text]);
  return (
    <pre
      ref={box}
      className="no-bar m-0 overflow-x-auto rounded-md bg-fill px-3 py-2 text-[13px] whitespace-pre"
    >
      <code>
        <Code text={text} lang={lang} />
      </code>
    </pre>
  );
}

/** A page of 应用管理: its header (back, title, its key) and its scrolling body. */
function Page({
  title,
  onBack,
  backLabel,
  busy,
  right,
  children,
}: {
  title: string;
  onBack: () => void;
  backLabel: string;
  busy?: boolean;
  right?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey
          label={backLabel}
          onClick={onBack}
          tipSide="bottom"
          className="size-7"
        >
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">
          {title}
        </h1>
        {busy && <LoaderCircle size={14} className="animate-spin text-ink-4" />}
        {right}
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col gap-5 px-3">{children}</div>
      </div>
    </div>
  );
}

/** Makes computer id the caller's (or o's), or nobody's. */
export async function setOwner(qc: QueryClient, id: string, o: SetOwner) {
  try {
    await api.setOwner(id, o);
    void qc.invalidateQueries({ queryKey: ["apps"] });
  } catch (e) {
    toast(t("err.action"), { error: true, description: errText(e) });
  }
}

/** Asks computer a's for g (api.grantMachine): anything more is confirmed at its terminal, which
 *  the toast says while it waits; true once given. */
export function useGrant(a: App) {
  const qc = useQueryClient();
  return async (g: MachineGrant) => {
    const more = g.shell === true || !!g.roots?.length;
    const waiting = more
      ? toast(t("grant.waiting", { name: a.name }), { timeout: 90000 })
      : "";
    try {
      await api.grantMachine(a.id, g);
      void qc.invalidateQueries({ queryKey: ["apps"] });
      if (more) toast(t("grant.done"));
      return true;
    } catch (e) {
      const code =
        e instanceof ApiError
          ? (e.body as { code?: string } | undefined)?.code
          : undefined;
      toast(
        code === "declined"
          ? t("grant.declined", { name: a.name })
          : code === "no_grant"
            ? t("grant.noGrant", { name: a.name })
            : t("err.action"),
        { error: true, description: code ? undefined : errText(e) },
      );
      return false;
    } finally {
      if (waiting) toasts.close(waiting);
    }
  };
}

/** 开放文件夹, under a computer's row: the folders it serves now, and a whole path there to add
 *  (confirmed at its terminal). Enter adds it, Escape closes. */
export function OpenFolder({
  a,
  m,
  grant,
  done,
}: {
  a: App;
  m: Computer;
  grant: (g: MachineGrant) => Promise<boolean>;
  done: () => void;
}) {
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false);
  const add = async () => {
    const p = path.trim();
    if (!p || busy) return;
    setBusy(true);
    const ok = await grant({ roots: [p] });
    setBusy(false);
    if (ok) done();
  };
  return (
    <div ref={intoView} className="g-fade py-2">
      <CardFrame label={t("grant.addFolder")}>
        <CardHead
          icon={<FolderOpen size={16} />}
          title={t("grant.addFolder")}
          tag={a.name}
          summary={`${t("grant.folders")}：${m.roots?.length ? m.roots.join("、") : t("grant.allFolders")}`}
          keys={
            <>
              <SecondKey disabled={busy} onClick={done}>
                {t("me.cancel")}
              </SecondKey>
              <MainKey
                busy={busy}
                disabled={busy || !path.trim()}
                onClick={() => void add()}
              >
                {t("grant.allow")}
              </MainKey>
            </>
          }
          extra={
            <input
              autoFocus
              value={path}
              placeholder={
                m.home ? `${m.home}${m.sep || "/"}…` : t("grant.folderPath")
              }
              aria-label={t("grant.folderPath")}
              onChange={(e) => setPath(e.target.value)}
              onKeyDown={(e) => {
                if (e.nativeEvent.isComposing) return;
                if (e.key === "Enter") void add();
                if (e.key === "Escape") done();
              }}
              className="no-ring h-8 w-full min-w-0 rounded-md bg-fill px-2 font-mono text-[13px] select-text"
            />
          }
        />
      </CardFrame>
    </div>
  );
}

/** A row's menu, at the pointer on a right click, under the row on a long press. */
export function useRowMenu() {
  const [at, setAt] = useMenuAt();
  const box = useRef<HTMLDivElement>(null);
  const lp = useLongPress(() => {
    const r = box.current?.getBoundingClientRect();
    if (r) setAt({ x: r.left + 16, y: r.bottom });
  });
  const anchor = usePointAnchor(at);
  return {
    at,
    on: {
      ref: box,
      ...lp,
      onContextMenu: (e: React.MouseEvent) =>
        !lp.onContextMenu(e) &&
        own(e) &&
        (e.preventDefault(), setAt({ x: e.clientX, y: e.clientY })),
    },
    menu: (items: MenuItem[]) => (
      <Menu.Root open={at !== null} onOpenChange={(o) => !o && setAt(null)}>
        <MenuPortal>
          <Menu.Positioner
            anchor={anchor}
            side="bottom"
            align="start"
            sideOffset={4}
            collisionPadding={8}
            className="z-50"
          >
            <MenuItems items={items} />
          </Menu.Positioner>
        </MenuPortal>
      </Menu.Root>
    ),
  };
}

/** A new face for app or login id: a picture chosen, cropped in place under the row, saved. */
/** A face picked from a file, cropped in place (Crop), and given to upload; the one implementation
 *  of changing a face (docs/前端设计规范.md "点头像就是换头像"). */
export function useFacePick(
  upload: (b: Blob) => Promise<unknown>,
  label = t("term.avatar"),
) {
  const [picture, setPicture] = useState<string | null>(null);
  const file = useRef<HTMLInputElement>(null);
  const drop = () =>
    setPicture((old) => (old && URL.revokeObjectURL(old), null));
  const save = async (blob: Blob) => {
    try {
      await upload(blob);
      drop();
    } catch (e) {
      toast(label, { error: true, description: errText(e) });
    }
  };
  return {
    open: () => file.current?.click(),
    input: (
      <input
        ref={file}
        type="file"
        accept="image/*"
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f)
            setPicture(
              (old) => (
                old && URL.revokeObjectURL(old),
                URL.createObjectURL(f)
              ),
            );
          e.target.value = "";
        }}
      />
    ),
    crop: picture && (
      <div className="g-fade flex justify-center py-2">
        <Crop src={picture} onCancel={drop} onDone={(b) => void save(b)} />
      </div>
    ),
  };
}

/** Removes app, ir_web's login or invite id; false if it failed. */
export async function revoke(qc: QueryClient, id: string, name: string) {
  let ok = true;
  try {
    await api.revoke(id);
    toast(t("term.revoked", { name }));
  } catch (e) {
    ok = false;
    toast(t("err.revoke"), { error: true, description: errText(e) });
  }
  await qc.invalidateQueries({ queryKey: ["apps"] });
  return ok;
}

/** The marks of the devices a browser of 网页 is on (api.Device); "none" shows none. */
const DEVICE_MARKS: Record<Device, LucideIcon> = {
  computer: Monitor,
  laptop: Laptop,
  phone: Smartphone,
  none: EyeOff,
};
const DEVICE_CHOICES: Device[] = ["computer", "laptop", "phone", "none"];

/** A login's avatar's corner: the device it was last on (its browser's passkey's). */
export const deviceBadge = (d?: Device) =>
  d && d !== "none"
    ? { badge: DEVICE_MARKS[d], badgeLabel: t(`device.${d}`) }
    : {};

/** A day as "11-10". */
const day = (ms: number) => {
  const d = new Date(ms);
  return `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
};

/** A login's passkeys, one per device (docs/web.md "Passkey"): each its device's mark (pressed, a
 *  menu to set it, or 自动: by the browser), when made and last used, its ✕ (not the last one's: a
 *  login keeps one; remove the login instead). + top right: a one-time link that adds one, shown
 *  once here; a link not used yet is a row of its own till it is. */
/** A login's passkeys, asked for ahead by whatever shows their count. */
export const passkeysQ = (id: string) => ({ queryKey: ["passkeys", id], queryFn: () => api.passkeys(id) });

/** A login's passkeys and the links waiting for a new device; add: a link for one more. */
export function usePasskeys(login: Login) {
  const qc = useQueryClient();
  const q = useQuery(passkeysQ(login.id));
  const at = `passkeys:${login.id}`;
  const changed = () => {
    void qc.invalidateQueries({ queryKey: ["passkeys", login.id] });
    void qc.invalidateQueries({ queryKey: ["apps"] });
  };
  const add = async () => {
    try {
      showSecret({ invite: await api.addPasskey(login.id), passkey: true, at });
      changed();
    } catch (e) {
      toast(t("passkey.add"), { error: true, description: errText(e) });
    }
  };
  return { q, at, changed, add };
}

/** The rows of a login's passkeys, a new device's link shown once above them. */
export function PasskeyList({ login }: { login: Login }) {
  useLang();
  const { q, at, changed } = usePasskeys(login);
  const keys = q.data?.passkeys ?? [];
  return (
    <>
      <SecretHere at={at} />
      {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
      <section className="flex flex-col">
        {keys.map((k) => (
          <PasskeyRow key={k.id} login={login.id} k={k} last={keys.length === 1} changed={changed} />
        ))}
        {q.data?.links.map((l) => (
          <PasskeyLinkRow key={l.id} login={login.id} l={l} changed={changed} />
        ))}
      </section>
    </>
  );
}

export function PasskeysPage({ login, onBack }: { login: Login; onBack: () => void }) {
  useLang();
  const { q, add } = usePasskeys(login);
  return (
    <Page
      title={t("passkey.title", { name: login.name })}
      onBack={onBack}
      backLabel={t("term.panel")}
      busy={q.isFetching}
      right={
        <IconKey label={t("passkey.add")} onClick={() => void add()} tipSide="bottom" className="size-7">
          <Plus size={16} />
        </IconKey>
      }
    >
      <PasskeyList login={login} />
    </Page>
  );
}

function PasskeyRow({
  login,
  k,
  last,
  changed,
}: {
  login: string;
  k: Passkey;
  last: boolean;
  changed: () => void;
}) {
  const Mark = DEVICE_MARKS[k.device] ?? Monitor;
  const set = async (d: Device | "") => {
    try {
      await api.passkeyDevice(login, k.id, d);
      changed();
    } catch (e) {
      toast(t("passkey.device"), { error: true, description: errText(e) });
    }
  };
  const remove = async () => {
    try {
      await api.removePasskey(login, k.id);
      toast(t("passkey.removed", { name: k.name }));
      changed();
    } catch (e) {
      toast(t("passkey.remove"), { error: true, description: errText(e) });
    }
  };
  const chosen: Device | "" = k.auto ? "" : k.device;
  const item = (
    d: Device | "",
    Icon: LucideIcon,
    label: string,
    hint?: string,
  ) => (
    <Menu.Item
      key={d || "auto"}
      onClick={() => void set(d)}
      className="flex min-h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 py-1 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:min-h-11"
    >
      <Icon size={15} className="shrink-0 text-ink-2" />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate">{label}</span>
        {hint && <span className="text-xs text-ink-3">{hint}</span>}
      </span>
      {chosen === d && <Check size={15} className="shrink-0" />}
    </Menu.Item>
  );
  return (
    <div className="flex min-h-14 items-center gap-3 rounded-lg px-2">
      <Menu.Root>
        <Tip
          label={`${t("passkey.device")} · ${k.auto ? t("passkey.auto") : t(`device.${k.device}`)}`}
        >
          <Menu.Trigger
            aria-label={t("passkey.device")}
            className="g-press grid size-8 shrink-0 place-items-center rounded-lg bg-fill text-ink-2 outline-none hover:bg-fill-2 focus-visible:ring-2 focus-visible:ring-edge data-popup-open:bg-fill-2 data-popup-open:text-ink"
          >
            <Mark size={16} />
          </Menu.Trigger>
        </Tip>
        <MenuPortal>
          <Menu.Positioner
            side="bottom"
            align="start"
            sideOffset={4}
            collisionPadding={8}
            className="z-50"
          >
            <Menu.Popup
              finalFocus={false}
              className="g-unfold glass thick float max-w-[calc(100vw-16px)] min-w-48 rounded-lg p-1.5 text-ink outline-none"
            >
              <div className="flex flex-col">
                {item(
                  "",
                  MonitorSmartphone,
                  t("passkey.auto"),
                  t("passkey.autoHint"),
                )}
                <div className="mx-2 my-1 border-t border-edge" />
                {DEVICE_CHOICES.map((d) =>
                  item(d, DEVICE_MARKS[d], t(`device.${d}`)),
                )}
              </div>
            </Menu.Popup>
          </Menu.Positioner>
        </MenuPortal>
      </Menu.Root>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2">
          <span className="truncate text-base">{k.name}</span>
          {k.current && (
            <span className="shrink-0 text-xs text-ink-3">
              {t("term.this")}
            </span>
          )}
          {k.auto && (
            <span className="shrink-0 rounded-sm bg-fill px-1 text-[11px] leading-4 text-ink-2">
              {t("passkey.auto")}
            </span>
          )}
        </span>
        <span className="flex items-center gap-1.5 text-xs whitespace-nowrap text-ink-3 tabular-nums">
          <span className="truncate">
            {t("passkey.made", { date: day(k.createdAt) })} ·{" "}
            {t("passkey.used", { ago: ago(k.lastUsedAt) })}
          </span>
        </span>
      </span>
      {!last && (
        <SureKey
          label={t("passkey.remove")}
          sure={t("term.revokeSure")}
          run={() => void remove()}
          className="size-7"
        >
          <Trash2 size={14} />
        </SureKey>
      )}
    </div>
  );
}

/** A link to add a passkey not used yet: 等待新设备, till when, its ✕ takes it back. */
function PasskeyLinkRow({
  login,
  l,
  changed,
}: {
  login: string;
  l: PasskeyLink;
  changed: () => void;
}) {
  const drop = async () => {
    try {
      await api.dropPasskeyLink(login, l.id);
      changed();
    } catch (e) {
      toast(t("err.revoke"), { error: true, description: errText(e) });
    }
  };
  return (
    <div className="flex min-h-12 items-center gap-3 px-2">
      <Link2 size={15} className="mx-2 shrink-0 text-ink-3" />
      <span className="min-w-0 flex-1 truncate text-base text-ink-2">
        {t("passkey.waiting")}
      </span>
      <span className="text-xs text-ink-3 tabular-nums">
        {t("term.until", { time: clock(l.expiresAt) })}
      </span>
      <SureKey
        label={t("term.revoke")}
        sure={t("term.revokeSure")}
        run={() => void drop()}
        className="size-7"
      >
        <Trash2 size={14} />
      </SureKey>
    </div>
  );
}

/** The name in place, chosen whole: Enter or leaving saves it, Escape keeps the old one. */
export function Rename({
  id,
  name: was,
  done,
}: {
  id: string;
  name: string;
  done: () => void;
}) {
  const qc = useQueryClient();
  const [name, setName] = useState(was);
  const gone = useRef(false);
  const save = async () => {
    if (gone.current) return;
    gone.current = true;
    const to = name.trim();
    done();
    if (!to || to === was) return;
    try {
      await api.profile({ name: to }, id);
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      toast(t("term.renameIt"), { error: true, description: nameError(e) });
    }
  };
  return (
    <input
      autoFocus
      value={name}
      maxLength={12}
      aria-label={t("term.rename")}
      onFocus={(e) => e.currentTarget.select()}
      onChange={(e) => setName(e.target.value)}
      onBlur={() => void save()}
      onKeyDown={(e) => {
        if (e.nativeEvent.isComposing) return;
        if (e.key === "Enter") e.currentTarget.blur();
        if (e.key === "Escape") ((gone.current = true), done());
      }}
      className="no-ring h-7 w-36 min-w-0 rounded-md bg-fill px-2 text-base font-medium select-text"
    />
  );
}
