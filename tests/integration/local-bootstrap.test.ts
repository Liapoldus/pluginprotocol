import { afterAll, describe, expect, it } from "vitest";
import { fileURLToPath } from "node:url";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
let fixture: GoFixtureBinary | undefined;

describe("supervised plugin inherited bootstrap", () => {
  afterAll(async () => fixture?.cleanup());

  it("exchanges only public launch identities over inherited pipes before pinned mTLS", async () => {
    fixture = await buildGoFixture(root, "./tests/fixtures/local-bootstrap");
    const child = startGoFixture(fixture.executable, { cwd: root });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => { stdout += String(chunk); });
    child.stderr.on("data", (chunk) => { stderr += String(chunk); });
    const result = await new Promise<number>((resolve, reject) => {
      child.once("error", reject);
      child.once("exit", (code) => resolve(code ?? 1));
    });
    await stopChildProcess(child);

    expect(result, stderr).toBe(0);
    expect(JSON.parse(stdout)).toEqual({
      bootstrapCompleted: true,
      listenerInherited: true,
      mutualTLSCall: true,
      tamperedPinRejected: true,
    });
  }, 30_000);
});
