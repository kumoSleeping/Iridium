// What in a draft looks like a key or a password (plan "凭据": 输入框发出前看着像密钥时提示“存成凭据？”):
// the known prefixes (sk-…, ghp_…, AKIA…, a PEM private key), else a long run of high entropy that
// mixes capitals, small letters and digits (not a hash, a UUID, a path or a link). With the name it
// would go by.

export type Secretish = { value: string; start: number; end: number; name: string };

const KNOWN: [RegExp, string][] = [
  [/-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]+?-----END [A-Z ]*PRIVATE KEY-----/, "SSH_KEY"],
  [/\bsk-ant-[A-Za-z0-9_-]{20,}/, "ANTHROPIC_API_KEY"],
  [/\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}/, "OPENAI_API_KEY"],
  [/\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{30,}/, "GITHUB_TOKEN"],
  [/\bgithub_pat_[A-Za-z0-9_]{30,}/, "GITHUB_TOKEN"],
  [/\bglpat-[A-Za-z0-9_-]{20,}/, "GITLAB_TOKEN"],
  [/\bxox[abprs]-[A-Za-z0-9-]{10,}/, "SLACK_TOKEN"],
  [/\bAKIA[0-9A-Z]{16}\b/, "AWS_ACCESS_KEY_ID"],
  [/\bAIza[0-9A-Za-z_-]{35}/, "GOOGLE_API_KEY"],
  [/\bhf_[A-Za-z0-9]{30,}/, "HF_TOKEN"],
  [/\bnpm_[A-Za-z0-9]{36}/, "NPM_TOKEN"],
  [/\bir_[A-Za-z0-9_-]{20,}/, "IRIDIUM_TOKEN"],
];

/** Shannon entropy, bits per character. */
export function entropy(s: string): number {
  const n = new Map<string, number>();
  for (const c of s) n.set(c, (n.get(c) ?? 0) + 1);
  let h = 0;
  for (const k of n.values()) h -= (k / s.length) * Math.log2(k / s.length);
  return h;
}

function random(s: string): boolean {
  if (s.length < 24 || /^[0-9a-f-]+$/i.test(s) || s.includes("/") || s.includes(".") || /^[a-z_-]+$/i.test(s)) return false;
  const kinds = [/[a-z]/, /[A-Z]/, /[0-9]/].filter((r) => r.test(s)).length;
  return kinds === 3 && entropy(s) >= 4.2;
}

/** The first thing in text that looks like a secret, not one of skip (said not to be). */
export function findSecretish(text: string, skip: ReadonlySet<string> = new Set()): Secretish | null {
  for (const [re, name] of KNOWN) {
    const g = new RegExp(re.source, "g");
    for (let m; (m = g.exec(text)); ) if (!skip.has(m[0])) return { value: m[0], start: m.index, end: m.index + m[0].length, name };
  }
  const run = /[A-Za-z0-9_+=-]{24,}/g;
  for (let m; (m = run.exec(text)); ) {
    // A link's or a path's part is not one.
    const before = text.slice(Math.max(0, m.index - 1), m.index);
    if (before === "/" || before === "." || before === "{" || skip.has(m[0])) continue;
    if (random(m[0])) return { value: m[0], start: m.index, end: m.index + m[0].length, name: "SECRET" };
  }
  return null;
}
