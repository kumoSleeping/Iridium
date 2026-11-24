// A channel's first screen (src/chats.ts firstScreen): one answer, put where the page reads each
// part, so its parts are not asked for again.
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { queryClient, speakAs } from "../src/api/client";
import { heldLog } from "../src/chat/channel/log";
import { workKey } from "../src/chat/channel/lib";
import { chatOf, firstScreen, fresh, freshState, isWhole, stateAhead } from "../src/chats";
import { getCarried } from "../src/store";

const state = (id: string) => ({ id, agent: "claude", cwd: "/a", status: "idle", asks: [], tasks: [], queue: [], end: 9, proc: 1 });
const block = (at: string, kind: string, text: string, time: number) => ({ id: at, content: kind === "user" ? text : `<iridium:${kind}>${text}</iridium:${kind}>`, created_at: time });
let asked: string[] = [];
let answer: (url: string) => unknown;
beforeEach(() => {
  asked = [];
  speakAs("ir_web", "a");
  vi.stubGlobal("fetch", async (url: string) => {
    asked.push(url);
    const body = answer(url);
    return new Response(JSON.stringify(body ?? { message: "none" }), { status: body ? 200 : 422, headers: { "content-type": "application/json" } });
  });
});
afterEach(() => vi.unstubAllGlobals());

test("one answer brings the state, members, reads, channel log, agent states and work; nothing is asked twice", async () => {
  answer = () => ({
    session: state("s1"),
    members: { channel: "s1", multi: true, joined: true, members: [{ member: "agent:s1", session: "s1", on: [{ message: { from: ["*"] } }] }, { member: "agent:s2", session: "s2", on: [] }] },
    reads: { channel: "s1", end: 2, readers: [] },
    log: { data: [{ id: "1", content: "hello", created_at: 100, user: { id: "ir_web/a", name: "a" } }, { id: "2", content: "done", created_at: 300, user: { id: "agent:s2", nick: "b" }, _data: { session: "s2", from: 4, to: 8 } }], _data: { start: 1, end: 2, more: false } },
    sessions: { s2: state("s2") },
    work: [{ session: "s2", from: 4, to: 8, data: [block("5.0", "thinking", "hm", 250)] }],
  });
  const chat = chatOf("s1");
  chat.log.back = false;
  const p = firstScreen("s1");
  expect(firstScreen("s1")).toBe(p);
  // Its state asked for meanwhile comes with it.
  expect(await stateAhead("s1")).toBe(true);
  await p;
  expect(asked).toEqual(["/v1/internal/sessions/s1/first"]);
  expect(isWhole("s1")).toBe(true);
  expect(getCarried<{ id: string }>("session:s1")?.id).toBe("s1");
  expect(getCarried<{ id: string }>("session:s2")?.id).toBe("s2");
  expect(freshState("s2")).toBe(true);
  expect(getCarried<{ end: number }>("reads:s1")?.end).toBe(2);
  expect(chat.coming).toBeUndefined();
  expect(queryClient.getQueryData<{ multi: boolean }>(["members", "s1"])?.multi).toBe(true);
  const log = heldLog("s1")!;
  expect(log.msgs.map((m) => m.n)).toEqual([1, 2]);
  expect(fresh(log.came)).toBe(true);
  expect(queryClient.getQueryData<{ at: string }[]>(workKey(log.msgs[1]!.src!))?.map((b) => b.at)).toEqual(["5.0"]);
});

test("record mode asks for ?record=1 and populates the session transcript; failures stay safe", async () => {
  const chat = chatOf("s3");
  chat.log.back = false;
  answer = () => ({
    session: state("s3"),
    page: { data: [block("3.0", "user", "hello", 100), block("5.0", "assistant", "hi", 200)], _data: { start: 3, end: 9, more: false } },
  });
  await firstScreen("s3", chat, undefined, true);
  expect(asked).toEqual(["/v1/internal/sessions/s3/first?record=1"]);
  expect(chat.log.loaded && chat.log.blocks.map((b) => b.at)).toEqual(["3.0", "5.0"]);

  asked = [];
  answer = () => undefined;
  await firstScreen("s4");
  expect(asked).toEqual(["/v1/internal/sessions/s4/first"]);
  expect(getCarried("session:s4")).toBeUndefined();
  expect(chatOf("s4").log.loaded).toBe(false);
});
