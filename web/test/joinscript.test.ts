import { describe, expect, it } from "vitest";
import { joinScript } from "../src/apps";

// The 电脑 group's + (apps.tsx): lines a person pastes on the computer that joins.
describe("joinScript", () => {
  it("fetches ir-computer for the system and runs it with the flags, to start at login", () => {
    const mac = joinScript("mac", "https://ir.example.com", ' --roots "~/work"');
    expect(mac).toContain("releases/latest/download/ir-computer-darwin-$(uname -m | sed s/x86_64/amd64/)");
    expect(mac.split("\n").at(-1)).toBe('~/.local/bin/ir-computer https://ir.example.com --roots "~/work" --install');
    const win = joinScript("win", "https://ir.example.com", " --shell");
    expect(win).toContain('"$env:LOCALAPPDATA\\iridium"');
    expect(win).toContain("ir-computer-windows-$a.exe");
    expect(win.split("\n").at(-1)).toBe('& "$d\\ir-computer.exe" https://ir.example.com --shell --install');
  });
});
