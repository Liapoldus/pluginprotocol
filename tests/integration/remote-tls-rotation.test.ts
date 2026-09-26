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
let directory = "";
let credentials: {
  oldAddress: string;
  renewedAddress: string;
  serverName: string;
  serverIdentity: string;
  oldRootFile: string;
  renewedRootFile: string;
  overlapRootsFile: string;
  controlCertificate: string;
  controlKey: string;
  dataCertificate: string;
  dataKey: string;
};

describe("remote plugin TLS endpoint and credential rotation", () => {
  beforeAll(async () => {
    directory = await mkdtemp(join(tmpdir(), "liapoldus-remote-tls-rotation-"));
    [serverFixture, clientFixture] = await Promise.all([
      buildGoFixture(root, "./tests/fixtures/remote-tls-rotation-server"),
      buildGoFixture(root, "./tests/fixtures/remote-tls-rotation-client"),
    ]);
    server = startGoFixture(serverFixture.executable, { cwd: root }, [directory]);
    const lines = createInterface({ input: server.stdout });
    const line = await new Promise<string>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("TLS rotation fixture did not become ready")), 10_000);
      lines.once("line", (value) => {
        clearTimeout(timeout);
        resolve(value);
      });
      server?.once("error", reject);
      server?.once("exit", (code) => reject(new Error(`TLS rotation fixture exited (${code})`)));
    });
    credentials = JSON.parse(line);
  }, 30_000);

  afterAll(async () => {
    if (server) await stopChildProcess(server);
    await serverFixture?.cleanup();
    await clientFixture?.cleanup();
    if (directory) await rm(directory, { recursive: true, force: true });
  });

  it("reconnects to a renewed endpoint while old and new trust roots overlap, retaining identity authorization", async () => {
    const control = await connect("renewedAddress", "overlapRootsFile", "control");
    expect(control).toMatchObject({ connected: true, authorized: true, manifestName: "rotated-fixture" });

    const data = await connect("renewedAddress", "overlapRootsFile", "data");
    expect(data).toMatchObject({ connected: true, authorized: false, code: "PermissionDenied" });
  }, 20_000);

  it("rejects the old endpoint after retiring its root and never downgrades to insecure transport", async () => {
    const retired = await connect("oldAddress", "renewedRootFile", "control");
    expect(retired.connected).toBe(false);
    expect(retired.authorized).toBe(false);

    const renewed = await connect("renewedAddress", "renewedRootFile", "control");
    expect(renewed).toMatchObject({ connected: true, authorized: true, manifestName: "rotated-fixture" });
  }, 20_000);
});

async function connect(endpointKey: "oldAddress" | "renewedAddress", rootsKey: "overlapRootsFile" | "renewedRootFile", identity: "control" | "data"): Promise<{ connected: boolean; authorized: boolean; manifestName?: string; code?: string }> {
  if (!clientFixture) throw new Error("TLS rotation client fixture binary is not built");
  const certificate = credentials[`${identity}Certificate`];
  const key = credentials[`${identity}Key`];
  return new Promise((resolve, reject) => {
    const child: ChildProcessWithoutNullStreams = spawn(clientFixture!.executable, [
      credentials[endpointKey], credentials[rootsKey], credentials.serverName, credentials.serverIdentity,
      certificate, key,
    ], { cwd: root, stdio: "pipe" });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => { stdout += String(chunk); });
    child.stderr.on("data", (chunk) => { stderr += String(chunk); });
    child.once("error", reject);
    child.once("exit", (code) => {
      try {
        if (code !== 0) throw new Error(`TLS rotation client exited (${code}): ${stderr}`);
        resolve(JSON.parse(stdout));
      } catch (error) {
        reject(error);
      }
    });
  });
}
