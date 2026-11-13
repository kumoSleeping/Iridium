// An agent's words as Markdown (Streamdown, with the CJK fixes): quiet, no buttons of its own, code
// blocks in their language's colours (hl.tsx) with a copy key, links only to http(s), the reply's
// file links (ir:<id>, files.tsx Linked) and what it names (ir:session/…, ir:folder/…, mention.tsx
// NamedLink); Iridium's Satori elements are read as those (refs.ts fromSatori). Raw HTML
// shows as written: rehype-raw is not even bundled (vite.config.ts). One options object, so a
// message is not drawn again for each piece of a draft (Streamdown's memo).
import { cjk } from "@streamdown/cjk";
import { Check, Copy } from "lucide-react";
import { isValidElement, memo, useState, type ComponentProps, type ReactNode } from "react";
import { defaultComponents, defaultUrlTransform, Streamdown, type ExtraProps, type StreamdownProps } from "streamdown";
import type { FileRef } from "../api/types.gen";
import { t } from "../i18n";
import { cn } from "../ui";
import { Linked, LinkFiles } from "./files";
import { Code } from "./hl";
import { NamedLink } from "./mention";
import { draftWords, fromSatori, refOf } from "./refs";

export function CodeBlock({ className, children }: ComponentProps<"code"> & ExtraProps) {
  const [copied, setCopied] = useState(false);
  const code = String(children ?? "").replace(/\n$/, "");
  const lang = /language-(\S+)/.exec(className ?? "")?.[1];
  return (
    <div className="group/code relative my-1 rounded-md bg-fill">
      <button
        type="button"
        aria-label={copied ? t("msg.copied") : t("msg.copy")}
        title={lang}
        onClick={() => void navigator.clipboard.writeText(code).then(() => (setCopied(true), setTimeout(() => setCopied(false), 1500)))}
        className="g-press absolute top-1 right-1 grid size-7 place-items-center rounded-sm text-ink-3 opacity-0 transition-opacity group-hover/code:opacity-100 hover:bg-fill-2 hover:text-ink focus-visible:opacity-100 pointer-coarse:opacity-100"
      >
        {copied ? <Check size={14} /> : <Copy size={14} />}
      </button>
      {/* Its words clear of the key: a one-line block was written under it (the user, 2026-11-08).
          The language is the key's tip, not a label over the words. */}
      <pre className="overflow-x-auto py-2.5 pr-10 pl-3 whitespace-pre">
        <code>
          <Code text={code} lang={lang} />
        </code>
      </pre>
    </div>
  );
}

export const MARKDOWN: Partial<StreamdownProps> = {
  plugins: { cjk },
  controls: false,
  components: {
    // Streamdown's own pre marks the code inside as a block (data-block): that one is ours.
    code: (p) => ("data-block" in p ? <CodeBlock {...p} /> : <defaultComponents.code {...p} />),
    img: (p) => (linkID(p.src) ? <Linked id={linkID(p.src)!} label={p.alt ?? ""} inline={false} /> : <defaultComponents.img {...p} />),
    a: (p) => {
      if (linkID(p.href)) return <Linked id={linkID(p.href)!} label={textOf(p.children)} inline />;
      const r = typeof p.href === "string" ? refOf(p.href, textOf(p.children)) : undefined;
      return r ? <NamedLink m={r} /> : <defaultComponents.a {...p} />;
    },
  },
  lineNumbers: false,
  linkSafety: { enabled: false },
  className: "prose-ir min-w-0",
  rehypePlugins: [],
  tableMaxHeight: 0,
  urlTransform: (url, key, node) => (linkID(url) || refOf(url, "") ? url : defaultUrlTransform(url, key, node)),
  allowElement: (e) => e.tagName !== "a" || /^(https?|ir):/i.test(String(e.properties.href ?? "")),
  unwrapDisallowed: true,
};

/** The id of a file link (ir:<id>), if url is one. */
function linkID(url: unknown): string | undefined {
  return typeof url === "string" ? /^ir:([a-z2-7]{10})$/.exec(url)?.[1] : undefined;
}

/** The words of a link's children. */
function textOf(n: ReactNode): string {
  if (typeof n === "string" || typeof n === "number") return String(n);
  if (Array.isArray(n)) return n.map(textOf).join("");
  if (isValidElement<{ children?: ReactNode }>(n)) return textOf(n.props.children);
  return "";
}

/** What the agent said, whole. files: the reply's (Block.files), its file links among them. */
export const Markdown = memo(function Markdown({ text, className, session, files }: { text: string; className?: string; session?: string; files?: FileRef[] }) {
  const md = (
    <Streamdown mode="static" {...MARKDOWN} className={cn("prose-ir min-w-0 text-md", className)}>
      {text}
    </Streamdown>
  );
  return session ? (
    <LinkFiles session={session} files={files ?? []}>
      {md}
    </LinkFiles>
  ) : (
    md
  );
});

/** What the agent is writing: Markdown half written shows as it will be, its title line left out
 *  (draftWords). live: words still come. No caret after them (the user, 2026-11-06). */
export function Draft({ text, live }: { text: string; live: boolean }) {
  return (
    <Streamdown {...MARKDOWN} isAnimating={live} className="prose-ir min-w-0 text-md">
      {draftWords(fromSatori(text), live)}
    </Streamdown>
  );
}
