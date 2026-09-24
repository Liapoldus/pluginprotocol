import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
let serverFixture: GoFixtureBinary | undefined;
let clientFixture: GoFixtureBinary | undefined;
let server: ReturnType<typeof startGoFixture> | undefined;
let serverDirectory = "";
let credentials: { address: string; caFile: string; serverName: string; serverIdentity: string; clientCertificate: string; clientKey: string };

describe("remote plugin mTLS client", () => {
  beforeAll(async () => {
    serverDirectory = await mkdtemp(join(tmpdir(), "liapoldus-remote-mtls-test-"));
    [serverFixture, clientFixture] = await Promise.all([
      buildGoFixture(root, "./tests/fixtures/remote-mtls-server"),
      buildGoFixture(root, "./tests/fixtures/remote-mtls-client"),
    ]);
    server = startGoFixture(serverFixture.executable, { cwd: root }, [serverDirectory]);
    const lines = createInterface({ input: server.stdout });
    const line = await new Promise<string>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("remote mTLS fixture did not become ready")), 10_000);
      lines.once("line", (value) => {
        clearTimeout(timeout);
        resolve(value);
      });
      server?.once("error", reject);
      server?.once("exit", (code) => reject(new Error(`remote mTLS fixture exited (${code})`)));
    });
    credentials = JSON.parse(line);
  }, 30_000);

  afterAll(async () => {
    if (server) await stopChildProcess(server);
    await serverFixture?.cleanup();
    await clientFixture?.cleanup();
    if (serverDirectory) await rm(serverDirectory, { recursive: true, force: true });
  });

  it("performs handshake over verified TLS with a client certificate", async () => {
    const result = await runClient([
      credentials.address,
      credentials.caFile,
      credentials.serverName,
      credentials.serverIdentity,
      credentials.clientCertificate,
      credentials.clientKey,
    ]);
    expect(result.exitCode, result.stderr).toBe(0);
    expect(JSON.parse(result.stdout)).toEqual({ ok: true, plugin: "fixture" });
  }, 20_000);

  it("rejects a server certificate with a different plugin URI identity", async () => {
    const result = await runClient([
      credentials.address,
      credentials.caFile,
      credentials.serverName,
      "urn:liapoldus:plugin:other:replica:pod-1",
      credentials.clientCertificate,
      credentials.clientKey,
    ]);
    expect(result.exitCode).not.toBe(0);
    expect(result.stdout).not.toContain("private");
    expect(result.stderr).not.toContain("private");
  }, 20_000);
});

function runClient(args: string[]): Promise<{ exitCode: number; stdout: string; stderr: string }> {
  if (!clientFixture) throw new Error("remote client fixture binary is not built");
  return new Promise((resolve, reject) => {
    const child: ChildProcessWithoutNullStreams = spawn(clientFixture.executable, args, { cwd: root, stdio: "pipe" });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => { stdout += String(chunk); });
    child.stderr.on("data", (chunk) => { stderr += String(chunk); });
    child.once("error", reject);
    child.once("exit", (code) => resolve({ exitCode: code ?? 1, stdout, stderr }));
  });
}
