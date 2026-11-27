import { describe, expect, test } from "vitest";
import type { App, Login } from "../src/api/types.gen";
import { splitRecvPeople } from "../src/chat/channel/recv";

const app = (id: string, name: string, extra: Partial<App> = {}): App => ({ id, name, type: "bot", ...extra } as App);
const login = (a: string, id: string, name: string): Login => ({ app: a, id, name } as Login);

describe("splitRecvPeople", () => {
  const apps = [
    app("local", "本机", { host: true, type: "host" }),
    app("ir_web", "Ir-Web", { web: true, type: "web" }),
    app("koishi", "Koishi"),
    app("qq", "QQ"),
    app("hook", "Webhook"),
    app("pc1", "Mac", { type: "computer" }),
  ];
  const logins = [
    login("ir_web", "u1", "otae-c"),
    login("ir_web", "u2", "kumo"),
    login("koishi", "k1", "BotA"),
    login("koishi", "k2", "BotB"),
    login("qq", "q1", "QQBot"),
  ];

  test("drops ir_web/*, keeps web logins in main, groups non-web apps with <=1 login as direct", () => {
    const r = splitRecvPeople(["*"], apps, logins);
    expect(r.webLogins).toEqual([
      { entry: "ir_web/u1", name: "otae-c" },
      { entry: "ir_web/u2", name: "kumo" },
    ]);
    // Host and computers excluded; qq (1 login) and hook (0 logins) are direct toggles; koishi (2 logins) has sub-list.
    expect(r.apps.map((a) => ({ id: a.id, name: a.name, direct: a.direct, all: a.all, logins: a.logins }))).toEqual([
      {
        id: "koishi",
        name: "Koishi",
        direct: undefined,
        all: "koishi/*",
        logins: [
          { entry: "koishi/k1", name: "BotA" },
          { entry: "koishi/k2", name: "BotB" },
        ],
      },
      { id: "qq", name: "QQ", direct: "qq/*", all: undefined, logins: [] },
      { id: "hook", name: "Webhook", direct: "hook/*", all: undefined, logins: [] },
    ]);
  });

  test("only one login of a multi-login app in channel collapses to direct toggle", () => {
    const r = splitRecvPeople(["ir_web/u1", "koishi/k2"], apps, logins);
    expect(r.webLogins).toEqual([{ entry: "ir_web/u1", name: "otae-c" }]);
    expect(r.apps.map((a) => ({ id: a.id, direct: a.direct, all: a.all, logins: a.logins }))).toEqual([
      { id: "koishi", direct: "koishi/k2", all: undefined, logins: [] },
    ]);
  });
});
