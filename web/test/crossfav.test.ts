import { describe, expect, it } from "vitest";
import { favKey, keyId, keyServer } from "../src/crossfav";

describe("cross favorites' keys", () => {
  it("are an Iridium's address and an id", () => {
    const k = favKey("http://127.0.0.1:7810", "dd7254b5");
    expect(keyServer(k)).toBe("http://127.0.0.1:7810");
    expect(keyId(k)).toBe("dd7254b5");
  });
});
