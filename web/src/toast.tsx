// Light toasts (plan "风格": frosted, popping in), one manager for the page: an action's receipt or
// error, a turn done elsewhere with a key to open it.
import { Toast } from "@base-ui/react/toast";
import { CircleAlert } from "lucide-react";
import type { Agent } from "./api/types.gen";
import { t } from "./i18n";
import { Logo } from "./ui";

export const toasts = Toast.createToastManager();

type Data = { agent?: Agent; jump?: string; error?: boolean };

/** A toast: title, maybe a line after it; error ones stay 8 s. */
export function toast(title: string, opts: { description?: string; error?: boolean; agent?: Agent; jump?: string; timeout?: number } = {}) {
  toasts.add({ title, description: opts.description, timeout: opts.timeout ?? (opts.error ? 8000 : 2500), data: { agent: opts.agent, jump: opts.jump, error: opts.error } satisfies Data });
}

/** An error's words for a toast. */
export const errText = (e: unknown) => (e instanceof TypeError ? t("err.offline") : e instanceof Error ? e.message : String(e));

export function ToastList({ onJump }: { onJump: (id: string) => void }) {
  const { toasts: list } = Toast.useToastManager();
  return list.map((toast) => {
    const d = toast.data as Data | undefined;
    return (
      <Toast.Root key={toast.id} toast={toast} className="g-pop-t glass thick float flex h-10 max-w-full items-center gap-2.5 rounded-full pr-1.5 pl-3.5 text-sm text-ink">
        {d?.agent && <Logo agent={d.agent} size={15} />}
        {d?.error && <CircleAlert size={15} className="shrink-0 text-err" />}
        <Toast.Title className="min-w-0 truncate font-medium" />
        <Toast.Description className="min-w-0 shrink truncate text-ink-2" />
        {d?.jump ? (
          <Toast.Close onClick={() => onJump(d.jump!)} className="g-press ml-1 h-7 shrink-0 rounded-full bg-fill px-3 text-xs font-medium hover:bg-fill-2">
            {t("toast.open")}
          </Toast.Close>
        ) : (
          <span className="w-2" />
        )}
      </Toast.Root>
    );
  });
}
