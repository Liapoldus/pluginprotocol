import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { afterAll, describe, expect, it } from "vitest";
import { buildGoFixture, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);
let gateway: GoFixtureBinary | undefined;
let plugin: GoFixtureBinary | undefined;

describe("SDK supervised-local session", () => {
  afterAll(async () => {
    await gateway?.cleanup();
    await plugin?.cleanup();
  });

  it("owns fd handoff, typed bootstrap, pinned mTLS, health, RPC and process stop", async () => {
    plugin = await buildGoFixture(root, "./tests/fixtures/local-session-plugin");
    gateway = await buildGoFixture(root, "./tests/fixtures/local-session-gateway");
    const { stdout, stderr } = await execFileAsync(gateway.executable, [plugin.executable], { cwd: root, timeout: 20_000 });

    expect(stderr).toBe("");
    expect(JSON.parse(stdout)).toEqual({
      call: true,
      health: true,
      processStopped: true,
    });
  }, 30_000);
});
