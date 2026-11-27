import { describe, expect, it } from "vitest";
import type { Session } from "../src/api/types.gen";
import { railStatus } from "../src/railstatus";

const s = (status: string, more: Partial<Session> = {}) => ({ id: status + Math.random(), status, ...more }) as unknown as Session;

describe("railStatus", () => {
  it("is quiet with nothing going on", () => {
    expect(railStatus([s("idle")], 0)).toEqual({ running: false, asks: 0, unread: false });
  });
  it("counts what runs, waits on you and is unread", () => {
    const list = [s("running"), s("waiting"), s("elsewhere", { needs: "permission" } as Partial<Session>), s("idle")];
    expect(railStatus(list, 3)).toEqual({ running: true, asks: 2, unread: true });
  });
  it("leaves archived sessions out", () => {
    expect(railStatus([s("waiting", { archived: true } as Partial<Session>)], 0).asks).toBe(0);
  });
});
