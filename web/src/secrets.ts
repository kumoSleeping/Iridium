// The one secret on screen: a new panel's invite link (with its QR code) or a plugin's token, shown
// once to the panel that made or allowed it (plan "ir": the token never reaches an agent). Kept in
// memory only, till its card closes; src/secret.tsx draws it, where it was made or allowed (at: the
// session whose card allowed it, or "terminals", the 终端 page).
import { useSyncExternalStore } from "react";
import type { Invite, Token } from "./api/types.gen";

export type Secret = ({ invite: Invite } | { token: Token }) & { at: string; key: number };

let current: Secret | null = null;
const subs = new Set<() => void>();

let n = 0;

export function showSecret(s: ({ invite: Invite } | { token: Token }) & { at: string } | null) {
  current = s && { ...s, key: ++n };
  subs.forEach((f) => f());
}

export const useSecret = () =>
  useSyncExternalStore(
    (f) => (subs.add(f), () => void subs.delete(f)),
    () => current,
  );
