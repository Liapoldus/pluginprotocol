import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

describe("generic peer stream half-close", () => {
  it("closes only the sender direction and still receives the peer response", () => {
    const output = execFileSync("go", ["run", "./tests/fixtures/stream-half-close"], {
      cwd: root,
      env: { ...process.env, GOWORK: "off" },
      encoding: "utf8",
    });

    expect(JSON.parse(output)).toEqual({
      remoteReadEOF: true,
      reply: "reply-after-half-close",
      serverSendError: "",
    });
  }, 60_000);
});
