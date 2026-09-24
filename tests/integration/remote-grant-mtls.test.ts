import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { once } from "node:events";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
let server: ChildProcessWithoutNullStreams;
let serverFixture: GoFixtureBinary | undefined;
let clientFixture: GoFixtureBinary | undefined;
let descriptor: Record<string, string>;

async function runClient(mode: string): Promise<{ code: number | null; stdout: string }> {
  return new Promise((resolve, reject) => {
    const child = spawn(clientFixture!.executable, [JSON.stringify(descriptor), mode], { cwd: root, stdio: "pipe" });
    let stdout = "";
    child.stdout.on("data", (chunk) => { stdout += String(chunk); });
    child.stderr.on("data", () => {});
    child.once("error", reject);
    child.once("exit", (code) => resolve({ code, stdout }));
  });
}

describe("remote GrantBroker mTLS callback", () => {
  beforeAll(async () => {
    serverFixture = await buildGoFixture(root, "./tests/fixtures/grant-remote-server");
    clientFixture = await buildGoFixture(root, "./tests/fixtures/grant-remote-client");
    server = startGoFixture(serverFixture.executable, { cwd: root });
    const lines = createInterface({ input: server.stdout });
    const [line] = await Promise.race([
      once(lines, "line"),
      once(server, "exit").then(([code]) => Promise.reject(new Error(`remote broker exited: ${code}`))),
    ]);
    descriptor = JSON.parse(String(line)) as Record<string, string>;
  }, 35_000);

  afterAll(async () => {
    if (server) await stopChildProcess(server);
    await serverFixture?.cleanup();
    await clientFixture?.cleanup();
  });

  it("returns only typed redemption secret bytes to the authorized replica", async () => {
    const result = await runClient("authorized");
    expect(result.code).toBe(0);
    expect(JSON.parse(result.stdout)).toEqual({ redeemed: true });
    expect(result.stdout).not.toContain("fixture-secret");
  });

  it("rejects an unregistered URI identity even when its client certificate chains to the trusted CA", async () => {
    const result = await runClient("wrong-client");
    expect(result.code).not.toBe(0);
    expect(result.stdout).toBe("");
  });

  it("rejects a trusted server certificate with the wrong Gateway URI identity", async () => {
    const result = await runClient("wrong-server");
    expect(result.code).not.toBe(0);
    expect(result.stdout).toBe("");
  }, 10_000);

  it("does not fall back to an unauthenticated channel when the client certificate is missing", async () => {
    const result = await runClient("no-client-certificate");
    expect(result.code).not.toBe(0);
    expect(result.stdout).toBe("");
  });
});
