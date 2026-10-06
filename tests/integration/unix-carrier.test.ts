import { execFileSync } from "node:child_process";
import { chmod, lstat, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { platform, tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { fileURLToPath } from "node:url";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
const FIXTURE_PACKAGE = "./tests/fixtures/peer-net";

interface FixtureReport {
  ok: boolean;
  addr?: string;
  error?: string;
  scenario?: string;
  received?: string[];
  carrier?: string;
  securityProfile?: string;
  encrypted?: boolean;
  authenticated?: boolean;
}

let fixture: GoFixtureBinary | undefined;
let temporaryDirectory: string | undefined;
let socketEndpoint: string;
let certificatesDirectory: string;
let server: ReturnType<typeof startGoFixture> | undefined;

async function runFixture(args: string[]): Promise<{ report: FixtureReport; code: number | null; stderr: string }> {
  const child = startGoFixture(fixture!.executable, {}, args);
  const stderr: string[] = [];
  child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk.toString()));
  const lines = createInterface({ input: child.stdout });
  const report = await new Promise<FixtureReport>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${args.join(" ")} did not report`)), 30_000);
    lines.once("line", (line) => {
      clearTimeout(timer);
      resolve(JSON.parse(line) as FixtureReport);
    });
    child.once("error", reject);
  });
  const code = await new Promise<number | null>((resolve) => {
    if (child.exitCode !== null) {
      resolve(child.exitCode);
      return;
    }
    child.once("exit", resolve);
  });
  return { report, code, stderr: stderr.join("") };
}

describe.skipIf(platform() === "win32")("Unix-domain peer carrier", () => {
  beforeAll(async () => {
    fixture = await buildGoFixture(root, FIXTURE_PACKAGE);
    temporaryDirectory = await mkdtemp(join(tmpdir(), "liapoldus-peer-unix-"));
    certificatesDirectory = join(temporaryDirectory, "certificates");
    socketEndpoint = `unix://${join(temporaryDirectory, "peer.sock")}`;
    execFileSync(fixture.executable, ["certs", "--dir", certificatesDirectory], { encoding: "utf8" });

    server = startGoFixture(fixture.executable, {}, [
      "server",
      "--carrier",
      "unix",
      "--security",
      "mtls",
      "--dir",
      certificatesDirectory,
      "--addr",
      socketEndpoint,
    ]);
    const lines = createInterface({ input: server.stdout });
    const ready = await new Promise<FixtureReport>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("Unix-domain server did not announce readiness")), 15_000);
      lines.once("line", (line) => {
        clearTimeout(timer);
        resolve(JSON.parse(line) as FixtureReport);
      });
      server!.once("error", reject);
    });
    expect(ready.ok).toBe(true);
    expect(ready.carrier).toBe("unix");
    expect(ready.encrypted).toBe(true);
    expect(ready.authenticated).toBe(true);
    socketEndpoint = ready.addr!;
    const socketInfo = await lstat(join(temporaryDirectory, "peer.sock"));
    expect(socketInfo.isSocket()).toBe(true);
    expect(socketInfo.mode & 0o777).toBe(0o600);
  }, 60_000);

  afterAll(async () => {
    if (server) {
      await stopChildProcess(server);
      await expect(lstat(join(temporaryDirectory!, "peer.sock"))).rejects.toMatchObject({ code: "ENOENT" });
    }
    if (fixture) await fixture.cleanup();
    if (temporaryDirectory) await rm(temporaryDirectory, { recursive: true, force: true });
  });

  it("uses mTLS for unary calls and bidirectional streams between processes", async () => {
    const baseArgs = ["client", "--addr", socketEndpoint, "--carrier", "unix", "--security", "mtls", "--dir", certificatesDirectory];
    const unary = await runFixture([...baseArgs, "--scenario", "unary"]);
    expect(unary.code, `${unary.stderr} ${unary.report.error ?? ""}`).toBe(0);
    expect(unary.report.ok).toBe(true);
    expect(unary.report.carrier).toBe("unix");
    expect(unary.report.encrypted).toBe(true);
    expect(unary.report.authenticated).toBe(true);

    const stream = await runFixture([...baseArgs, "--scenario", "stream"]);
    expect(stream.code, stream.stderr).toBe(0);
    expect(stream.report.ok).toBe(true);
    expect(stream.report.received).toEqual(["a", "b", "c"]);
  }, 60_000);

  it("is selectable through the public presentation facade", async () => {
    const endpoint = `unix://${join(temporaryDirectory!, "facade.sock")}`;
    const result = await runFixture([
      "facade",
      "--carrier",
      "unix",
      "--security",
      "mtls",
      "--dir",
      certificatesDirectory,
      "--endpoint",
      endpoint,
    ]);
    expect(result.code, `${result.stderr} ${result.report.error ?? ""}`).toBe(0);
    expect(result.report.ok).toBe(true);
    expect(result.report.carrier).toBe("unix");
    expect(result.report.echo).toBe("facade");
    expect(result.report.received).toEqual(["x", "y", "z"]);
  });

  it("refuses the plaintext development profile instead of downgrading", async () => {
    const result = await runFixture([
      "client",
      "--addr",
      socketEndpoint,
      "--carrier",
      "unix",
      "--security",
      "loopback",
      "--scenario",
      "unary",
    ]);
    expect(result.code).toBe(1);
    expect(result.report.ok).toBe(false);
  });

  it("does not unlink a live socket when another listener tries to bind it", async () => {
    const result = await runFixture([
      "server",
      "--carrier",
      "unix",
      "--security",
      "mtls",
      "--dir",
      certificatesDirectory,
      "--addr",
      socketEndpoint,
    ]);
    expect(result.code).toBe(1);
    expect(result.report.ok).toBe(false);

    const client = await runFixture([
      "client",
      "--carrier",
      "unix",
      "--security",
      "mtls",
      "--dir",
      certificatesDirectory,
      "--addr",
      socketEndpoint,
      "--scenario",
      "unary",
    ]);
    expect(client.code, client.stderr).toBe(0);
    expect(client.report.ok).toBe(true);
  });

  it("replaces an unreachable stale socket in a protected directory", async () => {
    const stalePath = join(temporaryDirectory!, "stale.sock");
    const staleProducer = startGoFixture(fixture!.executable, {}, [
      "server", "--carrier", "unix", "--security", "mtls", "--dir", certificatesDirectory, "--addr", `unix://${stalePath}`,
    ]);
    const staleLines = createInterface({ input: staleProducer.stdout });
    const staleReady = await new Promise<FixtureReport>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("stale Unix listener did not announce readiness")), 15_000);
      staleLines.once("line", (line) => {
        clearTimeout(timer);
        resolve(JSON.parse(line) as FixtureReport);
      });
      staleProducer.once("error", reject);
    });
    expect(staleReady.ok).toBe(true);
    staleProducer.kill("SIGKILL");
    await new Promise<void>((resolve) => staleProducer.once("exit", () => resolve()));
    expect((await lstat(stalePath)).isSocket()).toBe(true);

    const replacementChild = startGoFixture(fixture!.executable, {}, [
      "server", "--carrier", "unix", "--security", "mtls", "--dir", certificatesDirectory, "--addr", `unix://${stalePath}`,
    ]);
    const lines = createInterface({ input: replacementChild.stdout });
    const ready = await new Promise<FixtureReport>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("replacement Unix listener did not announce readiness")), 15_000);
      lines.once("line", (line) => {
        clearTimeout(timer);
        resolve(JSON.parse(line) as FixtureReport);
      });
      replacementChild.once("error", reject);
    });
    expect(ready.ok).toBe(true);
    const replacement = await lstat(stalePath);
    expect(replacement.isSocket()).toBe(true);
    expect(replacement.mode & 0o777).toBe(0o600);
    await stopChildProcess(replacementChild);
  });

  it("refuses unsafe socket paths without replacing files or binding in writable directories", async () => {
    const regularFile = join(temporaryDirectory!, "not-a-socket");
    await writeFile(regularFile, "keep this file");
    const fileEndpoint = `unix://${regularFile}`;
    const fileAttempt = await runFixture([
      "server", "--carrier", "unix", "--security", "mtls", "--dir", certificatesDirectory, "--addr", fileEndpoint,
    ]);
    expect(fileAttempt.code).toBe(1);
    expect(await readFile(regularFile, "utf8")).toBe("keep this file");

    const writableDirectory = join(temporaryDirectory!, "unsafe");
    await mkdir(writableDirectory, { mode: 0o700 });
    await chmod(writableDirectory, 0o777);
    const unsafeEndpoint = `unix://${join(writableDirectory, "peer.sock")}`;
    const directoryAttempt = await runFixture([
      "server", "--carrier", "unix", "--security", "mtls", "--dir", certificatesDirectory, "--addr", unsafeEndpoint,
    ]);
    expect(directoryAttempt.code).toBe(1);
    expect(await lstat(writableDirectory).then((info) => info.isDirectory())).toBe(true);
    await chmod(writableDirectory, 0o700);
  });
});
