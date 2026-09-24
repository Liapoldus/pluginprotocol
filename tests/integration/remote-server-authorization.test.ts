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
let credentialDirectory = "";
let credentials: {
  address: string;
  caFile: string;
  serverName: string;
  serverIdentity: string;
  controlCertificate: string;
  controlKey: string;
  dataCertificate: string;
  dataKey: string;
  otherCertificate: string;
  otherKey: string;
};

describe("remote plugin server authorization", () => {
  beforeAll(async () => {
    credentialDirectory = await mkdtemp(join(tmpdir(), "liapoldus-remote-auth-test-"));
    [serverFixture, clientFixture] = await Promise.all([
      buildGoFixture(root, "./tests/fixtures/remote-authorized-server"),
      buildGoFixture(root, "./tests/fixtures/remote-authorized-client"),
    ]);
    server = startGoFixture(serverFixture.executable, { cwd: root }, [credentialDirectory]);
    const lines = createInterface({ input: server.stdout });
    const line = await new Promise<string>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("remote authorization fixture did not become ready")), 10_000);
      lines.once("line", (value) => {
        clearTimeout(timeout);
        resolve(value);
      });
      server?.once("error", reject);
      server?.once("exit", (code) => reject(new Error(`remote authorization fixture exited (${code})`)));
    });
    credentials = JSON.parse(line);
  }, 30_000);

  afterAll(async () => {
    if (server) await stopChildProcess(server);
    await serverFixture?.cleanup();
    await clientFixture?.cleanup();
    if (credentialDirectory) await rm(credentialDirectory, { recursive: true, force: true });
  });

  it("permits only control identity to invoke control RPCs", async () => {
    expect(await invoke("control", "manifest")).toBe(true);
    expect(await invoke("data", "manifest")).toBe(false);
  }, 20_000);

  it("requires a typed DispatchApply acknowledgement before enabling data calls", async () => {
    expect(await invoke("data", "call", "forms.submit")).toBe(false);
    const result = await invokeRaw("control", "dispatch");
    expect(result.accepted).toBe(true);
    expect(result.response).toMatchObject({
      generation: "1",
      replicaIdentityUri: "urn:liapoldus:plugin:forms:replica:pod-1",
      settingsDigest: expect.stringMatching(/^sha256:/),
      releaseDigest: expect.stringMatching(/^sha256:/),
      manifestDigest: expect.stringMatching(/^sha256:/),
      dispatchDigest: expect.stringMatching(/^sha256:/),
    });
    expect(await invoke("control", "dispatch")).toBe(true);
    expect(await invoke("control", "dispatch-conflict")).toBe(false);
    expect(await invoke("data", "call", "forms.submit")).toBe(true);
    expect(await invoke("data", "call", "forms.read")).toBe(false);
  }, 20_000);

  it("permits only the data identity and active capability for Call and Stream", async () => {
    expect(await invoke("data", "call", "forms.submit")).toBe(true);
    expect(await invoke("data", "call", "forms.read")).toBe(false);
    expect(await invoke("control", "call", "forms.submit")).toBe(false);
    expect(await invoke("data", "stream", "forms.submit")).toBe(true);
    expect(await invoke("control", "stream", "forms.submit")).toBe(false);
  }, 30_000);

  it("keeps standard health within the control identity scope", async () => {
    expect(await invoke("control", "health")).toBe(true);
    expect(await invoke("data", "health")).toBe(false);
  }, 20_000);

  it("rejects a certificate bound to another logical plugin instance", async () => {
    expect(await invoke("other", "manifest")).toBe(false);
  }, 20_000);
});

async function invoke(identity: "control" | "data" | "other", operation: string, capability = ""): Promise<boolean> {
	return (await invokeRaw(identity, operation, capability)).accepted === true;
}

async function invokeRaw(identity: "control" | "data" | "other", operation: string, capability = ""): Promise<{ accepted: boolean; response?: Record<string, unknown> }> {
  if (!clientFixture) throw new Error("remote authorization client fixture is not built");
  const certificate = credentials[`${identity}Certificate`];
  const key = credentials[`${identity}Key`];
  return new Promise((resolve, reject) => {
    const child: ChildProcessWithoutNullStreams = spawn(clientFixture.executable, [
      credentials.address,
      credentials.caFile,
      credentials.serverName,
      credentials.serverIdentity,
      certificate,
      key,
      operation,
      capability,
    ], { cwd: root, stdio: "pipe" });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => { stdout += String(chunk); });
    child.stderr.on("data", (chunk) => { stderr += String(chunk); });
    child.once("error", reject);
    child.once("exit", (code) => {
      try {
        if (code !== 0) throw new Error(`authorization client exited (${code}): ${stderr}`);
        resolve(JSON.parse(stdout));
      } catch (error) {
        reject(error);
      }
    });
  });
}
