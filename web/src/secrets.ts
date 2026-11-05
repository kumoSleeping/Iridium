// The one secret on screen: a new panel's invite link (with its QR code) or a plugin's token, shown
// once to the panel that made or allowed it (plan "ir": the token never reaches an agent). Kept in
// memory only, till its dialog closes; src/secret.tsx draws it.
import { useSyncExternalStore } from "react";
import type { Invite, Token } from "./api/types.gen";

export type Secret = { invite: Invite } | { token: Token };

let current: Secret | null = null;
const subs = new Set<() => void>();

export function showSecret(s: Secret | null) {
  current = s;
  subs.forEach((f) => f());
}

export const useSecret = () =>
  useSyncExternalStore(
    (f) => (subs.add(f), () => void subs.delete(f)),
    () => current,
  );
