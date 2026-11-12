// A file's preview, inside the overlay files.tsx opens (plan "网页", 2026-11-12): its first MB
// fetched by a Range, as Markdown drawn, a table, code in its language's colours or plain text.
// Markup (html, svg, xml) only ever as its source. Fetched apart, the first time one opens.
import { LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";
import { t } from "../i18n";
import { Code } from "./hl";
import { Markdown } from "./markdown";
import { parseTable, PREVIEW_BYTES, PREVIEW_ROWS, rangeTotal, textOf, type PreviewKind } from "./preview-lib";

type Got = { text: string; cut: boolean } | "binary" | "error";

/** The preview of the file at url (served inline), of kind (not pdf: that opens in a tab). */
export default function PreviewBody({ url, name, kind }: { url: string; name: string; kind: PreviewKind }) {
  const [got, setGot] = useState<Got | undefined>();
  useEffect(() => {
    const stop = new AbortController();
    setGot(undefined);
    fetch(url, { headers: { Range: `bytes=0-${PREVIEW_BYTES - 1}` }, credentials: "same-origin", signal: stop.signal })
      .then(async (r) => {
        // An empty file has no first byte to give.
        if (r.status === 416) return setGot({ text: "", cut: false });
        if (!r.ok) throw new Error(String(r.status));
        const bytes = new Uint8Array(await r.arrayBuffer());
        const total = r.status === 206 ? rangeTotal(r.headers.get("Content-Range")) : undefined;
        const cut = total !== undefined ? total > bytes.length : bytes.length > PREVIEW_BYTES;
        const text = textOf(cut ? bytes.subarray(0, PREVIEW_BYTES) : bytes, cut);
        setGot(text === null ? "binary" : { text, cut });
      })
      .catch(() => stop.signal.aborted || setGot("error"));
    return () => stop.abort();
  }, [url]);

  if (got === undefined)
    return (
      <div className="grid min-h-40 place-items-center text-ink-3" role="status" aria-label={t("msg.loading")}>
        <LoaderCircle size={18} className="animate-spin" />
      </div>
    );
  if (got === "error" || got === "binary") return <div className="grid min-h-40 place-items-center text-sm text-ink-3">{t(got === "error" ? "msg.unreadable" : "msg.noPreview")}</div>;
  return (
    <>
      <Shown text={got.text} name={name} kind={kind} cut={got.cut} />
      {got.cut && <div className="px-5 pt-1 pb-4 text-xs text-ink-3">{t("msg.previewCut")}</div>}
    </>
  );
}

function Shown({ text, name, kind, cut }: { text: string; name: string; kind: PreviewKind; cut: boolean }) {
  if (kind === "markdown") return <Markdown text={text} className="px-5 py-4" />;
  if (kind === "csv" || kind === "tsv") return <Table text={text} sep={kind === "csv" ? "," : "\t"} cut={cut} />;
  return (
    <pre className="px-4 py-3 font-mono text-sm leading-[1.55] break-words whitespace-pre-wrap">
      <code>{kind === "code" ? <Code text={text} lang={name} /> : text}</code>
    </pre>
  );
}

/** A table: its first row the head, kept at the top as the rest scrolls under it. */
function Table({ text, sep, cut }: { text: string; sep: string; cut: boolean }) {
  const { rows, more } = parseTable(text, sep, PREVIEW_ROWS, cut);
  const [head, ...body] = rows;
  if (!head) return <div className="grid min-h-40 place-items-center text-sm text-ink-3">{t("msg.noPreview")}</div>;
  const cols = Math.max(...rows.map((r) => r.length));
  const cell = "max-w-[360px] border-b border-edge px-2.5 py-1.5 text-left align-top break-words whitespace-pre-wrap";
  return (
    <>
      <table className="min-w-full border-separate border-spacing-0 text-sm tabular-nums">
        <thead>
          <tr>
            {Array.from({ length: cols }, (_, i) => (
              <th key={i} className={`${cell} sticky top-0 bg-paper font-medium text-ink`}>
                {head[i] ?? ""}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {body.map((r, i) => (
            <tr key={i}>
              {Array.from({ length: cols }, (_, j) => (
                <td key={j} className={`${cell} text-ink-2`}>
                  {r[j] ?? ""}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      {more && <div className="px-5 pt-2 pb-1 text-xs text-ink-3">{t("msg.previewRows", { n: PREVIEW_ROWS })}</div>}
    </>
  );
}
