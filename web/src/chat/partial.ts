// A tool call's input as it is written (chat/live.tsx): pure, for tests.

/** The string fields of a JSON object cut anywhere, as far as they go: `{"command":"ls -l` is
 *  {command: "ls -l"}. Pure, for tests. */
export function partialInput(raw: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const m of raw.matchAll(/"([A-Za-z_][\w]*)"\s*:\s*"((?:[^"\\]|\\.)*)(\\?)/g)) {
    try {
      out[m[1]!] = JSON.parse(`"${m[2]}"`) as string;
    } catch {
      out[m[1]!] = m[2]!; // a \u cut short: as written
    }
  }
  return out;
}

/** The tail of what a call is about, so what is being typed shows: the last n letters, one line. */
export function tail(arg: string, n = 90): string {
  const one = arg.replace(/\s+/g, " ").trimStart();
  return one.length > n ? `…${one.slice(-n)}` : one;
}
