import { execFile } from "node:child_process";
import { createInterface } from "node:readline";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);
let serverFixture: GoFixtureBinary | undefined;
let server: ReturnType<typeof startGoFixture> | undefined;
let serverDirectory = "";
let credentials: { address: string; caFile: string; serverName: string; serverIdentity: string; clientCertificate: string; clientKey: string };

describe("remote plugin mTLS client", () => {
  beforeAll(async () => {
    serverDirectory = await mkdtemp(join(tmpdir(), "liapoldus-remote-mtls-test-"));
    serverFixture = await buildGoFixture(root, "./tests/fixtures/remote-mtls-server");
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
    if (serverDirectory) await rm(serverDirectory, { recursive: true, force: true });
  });

  it("performs handshake over verified TLS with a client certificate", async () => {
    const result = await execFileAsync("go", [
      "run", "./tests/fixtures/remote-mtls-client",
      credentials.address,
      credentials.caFile,
      credentials.serverName,
      credentials.serverIdentity,
      credentials.clientCertificate,
      credentials.clientKey,
    ], { cwd: root });
    expect(JSON.parse(result.stdout)).toEqual({ ok: true, plugin: "fixture" });
  });

  it("rejects a server certificate with a different plugin URI identity", async () => {
    const result = await execFileAsync("go", [
      "run", "./tests/fixtures/remote-mtls-client",
      credentials.address,
      credentials.caFile,
      credentials.serverName,
      "urn:liapoldus:plugin:other:replica:pod-1",
      credentials.clientCertificate,
      credentials.clientKey,
    ], { cwd: root, reject: false });
    expect(result.code).not.toBe(0);
    expect(result.stdout).not.toContain("private");
    expect(result.stderr).not.toContain("private");
  });
});
