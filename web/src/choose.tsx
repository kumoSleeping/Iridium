// A choice of a few, drawn as our own popup (the settings' rows). Apart from ui.tsx: Base UI's
// Select stays out of the first screen.
import { Select } from "@base-ui/react/select";
import { Check, ChevronDown } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "./ui";

/** A choice of a few, our own popup rather than the system's (the user, 2026-11-06): the key shows
 *  the one picked; its list unfolds under it, frosted like the menus, each its label and a faint
 *  hint, a check by the one picked. */
export function Choose({ value, options, onChange, label, disabled, className }: { value: string; options: { value: string; label: ReactNode; hint?: ReactNode }[]; onChange: (v: string) => void; label: string; disabled?: boolean; className?: string }) {
  return (
    <Select.Root value={value} onValueChange={(v) => v !== null && onChange(v as string)} items={options.map((o) => ({ value: o.value, label: o.label }))} disabled={disabled}>
      <Select.Trigger aria-label={label} className={cn("g-press flex h-7 max-w-48 min-w-0 cursor-default items-center gap-1 rounded-md bg-fill pr-1.5 pl-2.5 text-sm outline-none hover:bg-fill-2 disabled:opacity-40 data-popup-open:bg-fill-2", className)}>
        <Select.Value className="min-w-0 truncate" />
        <Select.Icon className="shrink-0 text-ink-3">
          <ChevronDown size={14} />
        </Select.Icon>
      </Select.Trigger>
      <Select.Portal>
        <Select.Positioner side="bottom" align="end" sideOffset={6} collisionPadding={8} alignItemWithTrigger={false} className="z-50">
          <Select.Popup className="g-unfold glass thick float max-h-(--available-height) min-w-(--anchor-width) overflow-y-auto rounded-lg p-1.5 text-ink outline-none">
            {options.map((o) => (
              <Select.Item key={o.value} value={o.value} className="flex min-h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 py-1 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:min-h-11">
                <span className="flex min-w-0 flex-1 flex-col">
                  <Select.ItemText>{o.label}</Select.ItemText>
                  {o.hint && <span className="text-xs text-ink-3">{o.hint}</span>}
                </span>
                <Select.ItemIndicator className="shrink-0 text-ink-2">
                  <Check size={14} />
                </Select.ItemIndicator>
              </Select.Item>
            ))}
          </Select.Popup>
        </Select.Positioner>
      </Select.Portal>
    </Select.Root>
  );
}
