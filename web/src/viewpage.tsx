// A public link's read-only view (docs/api.md "公开链接", /p/<token>; view.tsx loads it): the session's title and agent,
// then what people and the agent said, newest at the bottom, older pages as it is scrolled up,
// new ones by asking every few seconds while the page is seen. Its own entry (view.html): none
// of the panel's bootstrap, no login, no event stream; it asks only its link's own paths.
import { StrictMode, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import type { PublicMessage, PublicPage } from "./api/types.gen";
import { Picture } from "./chat/files";
import { Markdown } from "./chat/markdown";
import { withoutNameLines } from "./chat/refs";
import { t, useLang } from "./i18n";
import { agentName, cn, Logo } from "./ui";

/** The link's own root: /p/<token>. */
const ROOT = location.pathname.replace(/\/+$/, "");
const POLL = 4000;

/** A page of the messages; null once the link is gone (404). */
async function fetchPage(q = ""): Promise<PublicPage | null> {
  const r = await fetch(`${ROOT}/api/messages${q}`, { cache: "no-store", credentials: "omit" });
  if (r.status === 404) return null;
  if (!r.ok) throw new Error(String(r.status));
  return (await r.json()) as PublicPage;
}

/** a then b, each message once (by its place), in order. */
const merge = (a: PublicMessage[], b: PublicMessage[]) => {
  const seen = new Set(a.map((m) => m.at));
  return [...a, ...b.filter((m) => !seen.has(m.at))];
};

function View() {
  useLang();
  const [head, setHead] = useState<Pick<PublicPage, "title" | "agent">>();
  const [msgs, setMsgs] = useState<PublicMessage[]>([]);
  const [before, setBefore] = useState<string>();
  const [gone, setGone] = useState(false);
  const [older, setOlder] = useState(false);
  const end = useRef(0);
  const box = useRef<HTMLDivElement>(null);
  const top = useRef<HTMLDivElement>(null);
  // How the list is to stay as it changes: at the bottom, or where it was over older ones added.
  const keep = useRef<{ bottom: boolean; height: number }>({ bottom: true, height: 0 });

  const mark = () => {
    const el = box.current;
    if (el) keep.current = { bottom: el.scrollHeight - el.scrollTop - el.clientHeight < 80, height: el.scrollHeight };
  };
  const took = (p: PublicPage | null) => {
    if (!p) return setGone(true), false;
    setHead({ title: p.title, agent: p.agent });
    if (p.title) document.title = p.title;
    return true;
  };

  useEffect(() => {
    void fetchPage().then((p) => {
      if (!took(p)) return;
      end.current = p!.end;
      keep.current = { bottom: true, height: 0 };
      setMsgs(p!.messages);
      setBefore(p!.before || undefined);
    }, () => setGone(true));
  }, []);

  // New messages, while the page is seen.
  useEffect(() => {
    if (gone || !head) return;
    const tick = () => {
      if (document.visibilityState !== "visible") return;
      void fetchPage(`?after=${end.current}`).then((p) => {
        if (!took(p)) return;
        end.current = p!.end;
        if (p!.messages.length) {
          mark();
          setMsgs((m) => merge(m, p!.messages));
        }
      }, () => {});
    };
    const id = setInterval(tick, POLL);
    document.addEventListener("visibilitychange", tick);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [gone, head]);

  // Older ones as the top comes into sight.
  const more = useCallback(() => {
    if (!before || older) return;
    setOlder(true);
    void fetchPage(`?before=${before}`)
      .then((p) => {
        if (!took(p)) return;
        const el = box.current;
        keep.current = { bottom: false, height: el?.scrollHeight ?? 0 };
        setMsgs((m) => merge(p!.messages, m));
        setBefore(p!.before || undefined);
      }, () => {})
      .finally(() => setOlder(false));
  }, [before, older]);
  useEffect(() => {
    const el = top.current;
    if (!el || !before) return;
    const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && more(), { root: box.current, rootMargin: "200px 0px 0px 0px" });
    io.observe(el);
    return () => io.disconnect();
  }, [before, more]);

  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    if (keep.current.bottom) el.scrollTop = el.scrollHeight;
    else el.scrollTop += el.scrollHeight - keep.current.height;
    keep.current = { bottom: false, height: el.scrollHeight };
  }, [msgs]);

  return (
    <div ref={box} className="h-full overflow-y-auto">
      <header className="glass sticky top-0 z-10 shadow-[0_0.5px_0_var(--edge)]">
        <div className="mx-auto flex h-12 max-w-3xl items-center gap-2.5 px-4">
          {head && <Logo agent={head.agent} size={18} />}
          <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{head ? head.title || agentName(head.agent) : ""}</h1>
          <span className="shrink-0 rounded-sm bg-fill px-1.5 py-0.5 text-xs text-ink-3">{t("view.readonly")}</span>
        </div>
      </header>
      <main className="mx-auto flex max-w-3xl flex-col gap-5 px-4 pt-4 pb-10">
        <div ref={top} aria-hidden className="h-px" />
        {older && <p className="m-0 text-center text-sm text-ink-3">{t("view.loading")}</p>}
        {gone ? (
          <p className="m-0 py-16 text-center text-ink-3">{t("view.gone")}</p>
        ) : !head ? (
          <p className="m-0 py-16 text-center text-ink-3">{t("view.loading")}</p>
        ) : !msgs.length ? (
          <p className="m-0 py-16 text-center text-ink-3">{t("view.empty")}</p>
        ) : (
          msgs.map((m) => <Message key={m.at} m={m} />)
        )}
      </main>
    </div>
  );
}

function Message({ m }: { m: PublicMessage }) {
  const user = m.role === "user";
  const pictures = !!m.pictures?.length && (
    <div className={cn("flex flex-wrap gap-2", user && "justify-end")}>
      {m.pictures.map((p) => (
        <Picture key={p.url} src={p.url} full={p.url} name={p.name ?? ""} w={p.w} h={p.h} />
      ))}
    </div>
  );
  if (user)
    return (
      <div className="flex flex-col items-end gap-1.5 pl-10">
        {m.from && <span className="text-xs text-ink-3">{m.from}</span>}
        {m.text && <div className="max-w-[min(560px,100%)] rounded-xl rounded-tr-sm bg-fill px-3.5 py-2 text-md whitespace-pre-wrap [overflow-wrap:anywhere]">{m.text}</div>}
        {pictures}
      </div>
    );
  return (
    <div className="flex min-w-0 flex-col items-start gap-2 pr-6">
      {m.text && <Markdown text={withoutNameLines(m.text)} className="w-full" />}
      {pictures}
    </div>
  );
}

export function mount() {
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <View />
    </StrictMode>,
  );
}
