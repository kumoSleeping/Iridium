// Code in colours: a tool's command as bash, a file's text by its extension, a fence by its name,
// and what came back by a guess, kept only when sure. highlight.js (hl-lib.ts) is fetched apart the
// first time code is shown, not with the chat (test/build.test.ts): until then, and for a language
// not known, the text as it is.
import { useEffect, useMemo, useState } from "react";

type Lib = typeof import("./hl-lib");
let lib: Lib | undefined;
let loading: Promise<Lib> | undefined;
const load = () => (loading ??= import("./hl-lib").then((m) => (lib = m)));

/** lang: a language's name, a file's name, or "auto". label: the language's name at the top right,
 *  out of the way of the words. Colours: the hljs-* classes in styles.css. */
export function Code({ text, lang, label }: { text: string; lang?: string; label?: boolean }) {
  const [ready, setReady] = useState(!!lib);
  useEffect(() => {
    if (lang && !ready) void load().then(() => setReady(true));
  }, [lang, ready]);
  const lit = useMemo(() => (lang && ready && lib ? lib.highlight(text, lang) : null), [text, lang, ready]);
  if (!lit) return <>{text}</>;
  return (
    <>
      {label && <span className="float-right ml-3 text-[10px] leading-[18px] text-ink-4 select-none">{lit.lang}</span>}
      <span dangerouslySetInnerHTML={{ __html: lit.html }} />
    </>
  );
}
