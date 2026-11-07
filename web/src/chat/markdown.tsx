// An agent's words as Markdown (Streamdown, with the CJK fixes): quiet, no buttons of its own, code
// blocks as plain monospace with a copy key, links only to http(s). Raw HTML shows as written:
// rehype-raw is not even bundled (vite.config.ts). One options object, so a message is not drawn
// again for each piece of a draft (Streamdown's memo).
import { cjk } from "@streamdown/cjk";
import { Check, Copy } from "lucide-react";
import { memo, useState, type ComponentProps } from "react";
import { defaultComponents, Streamdown, type ExtraProps, type StreamdownProps } from "streamdown";
import { t } from "../i18n";
import { cn } from "../ui";
import { draftWords } from "./refs";

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
        <code>{code}</code>
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
  },
  lineNumbers: false,
  linkSafety: { enabled: false },
  className: "prose-ir min-w-0",
  rehypePlugins: [],
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
