// An agent's words as Markdown (Streamdown, with the CJK fixes): quiet, no buttons of its own, code
// blocks in their language's colours (hl.tsx) with a copy key, links only to http(s). Raw HTML
// shows as written: rehype-raw is not even bundled (vite.config.ts). One options object, so a
// message is not drawn again for each piece of a draft (Streamdown's memo). A session's id in it
// (or the start of one) goes there (mention.tsx Ids).
import { cjk } from "@streamdown/cjk";
import { Check, Copy } from "lucide-react";
import { memo, useState, type ComponentProps } from "react";
import { defaultComponents, Streamdown, type ExtraProps, type StreamdownProps } from "streamdown";
import { t } from "../i18n";
import { cn } from "../ui";
import { Code } from "./hl";
import { Ids } from "./mention";
import { draftWords, SID } from "./refs";

function CodeBlock({ className, children }: ComponentProps<"code"> & ExtraProps) {
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

type Node = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: Node[] };

/** Text that may name a session by id, outside code and links, put in an ir-ids element: Ids then
 *  finds which, with the sessions as they are now. */
function rehypeIds() {
  const walk = (n: Node) => {
    if (!n.children || n.tagName === "pre" || n.tagName === "code" || n.tagName === "a") return;
    n.children = n.children.map((c) => {
      if (c.type !== "text") return (walk(c), c);
      // search, not test: test moves the shared pattern's lastIndex, where matchAll then starts.
      return (c.value ?? "").search(SID) >= 0 ? { type: "element", tagName: "ir-ids", properties: {}, children: [c] } : c;
    });
  };
  return walk;
}

const textOf = (c: unknown): string | undefined => (typeof c === "string" ? c : Array.isArray(c) && c.every((x) => typeof x === "string") ? c.join("") : undefined);

export const MARKDOWN: Partial<StreamdownProps> = {
  plugins: { cjk },
  controls: false,
  components: {
    // Streamdown's own pre marks the code inside as a block (data-block): that one is ours.
    code: (p) => {
      if ("data-block" in p) return <CodeBlock {...p} />;
      const text = textOf(p.children);
      return <defaultComponents.code {...p}>{text === undefined ? p.children : <Ids text={text} />}</defaultComponents.code>;
    },
    ...({ "ir-ids": ({ children }: { children?: unknown }) => <Ids text={textOf(children) ?? ""} /> } as object),
  },
  lineNumbers: false,
  linkSafety: { enabled: false },
  className: "prose-ir min-w-0",
  rehypePlugins: [rehypeIds],
  tableMaxHeight: 0,
  allowElement: (e) => e.tagName !== "a" || /^https?:/i.test(String(e.properties.href ?? "")),
  unwrapDisallowed: true,
};

/** What the agent said, whole. */
export const Markdown = memo(function Markdown({ text, className }: { text: string; className?: string }) {
  return (
    <Streamdown mode="static" {...MARKDOWN} className={cn("prose-ir min-w-0 text-md", className)}>
      {text}
    </Streamdown>
  );
});

/** What the agent is writing: Markdown half written shows as it will be, its title line left out
 *  (draftWords). live: words still come. No caret after them (the user, 2026-11-06). */
export function Draft({ text, live }: { text: string; live: boolean }) {
  return (
    <Streamdown {...MARKDOWN} isAnimating={live} className="prose-ir min-w-0 text-md">
      {draftWords(text, live)}
    </Streamdown>
  );
}
