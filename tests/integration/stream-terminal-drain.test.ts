import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

describe("generic peer stream terminal delivery", () => {
  it("delivers every queued message before reporting clean completion", () => {
    const output = execFileSync("go", ["run", "./tests/fixtures/stream-terminal-drain"], {
      cwd: root,
      env: { ...process.env, GOWORK: "off" },
      encoding: "utf8",
      timeout: 60_000,
    });

    const report = JSON.parse(output) as { received: string[]; terminal: string };
    expect(report.received).toEqual(Array.from({ length: 32 }, (_, index) => `message-${index}`));
    expect(report.terminal).toBe("EOF");
  }, 75_000);
});
