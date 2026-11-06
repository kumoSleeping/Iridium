// Fetching ahead (plan "弱网"): once the page is idle, the files of the later parts (the chat, the
// settings, the jump box…) the first screen did not load, one at a time at low priority, as the HTML's
// <meta name="later"> lists them (vite.config.ts); then their code runs from the cache (app.tsx). Nothing while the browser saves data.
import { queryClient, request } from "./api/client";
import { mayPrefetch } from "./chats";

let idle: Promise<void> | undefined;
/** Resolves once the page has loaded, no answer is out and the browser is idle. */
export const settled = () =>
  (idle ??= new Promise<void>((resolve) => {
    const look = () => {
      if (document.readyState !== "complete" || queryClient.isFetching()) return void setTimeout(look, 200);
      if (typeof requestIdleCallback === "function") requestIdleCallback(() => resolve(), { timeout: 2_000 });
      else setTimeout(resolve, 200);
    };
    look();
  }));

/** Fetches the later files into the browser's cache; says whether every one came. */
export async function prefetch(): Promise<boolean> {
  const files = document.querySelector<HTMLMetaElement>('meta[name="later"]')?.content.split(" ").filter(Boolean) ?? [];
  await settled();
  let all = true;
  for (const f of files) {
    while (queryClient.isFetching()) await new Promise((r) => setTimeout(r, 500));
    if (!mayPrefetch()) return false;
    const came = await request(f, { priority: "low" } as RequestInit)
      .then(async (r) => (await r.blob(), r.ok))
      .catch(() => false);
    all &&= came;
  }
  return all;
}
