import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomUUID } from "node:crypto";
import { createInterface } from "node:readline";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { fileURLToPath } from "node:url";
import { startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
const PIPE_FIXTURE = "./tests/fixtures/windows-pipe";
const PEER_NET_FIXTURE = "./tests/fixtures/peer-net";

interface Report {
  ok: boolean;
  addr?: string;
  error?: string;
  message?: string;
  exists?: boolean;
  daclRestricted?: boolean;
  carrier?: string;
  encrypted?: boolean;
  authenticated?: boolean;
  received?: string[];
}

let pipeFixture: GoFixtureBinary | undefined;
let certFixture: GoFixtureBinary | undefined;
let stateDirectory: string | undefined;
let certificatesDirectory: string;
let endpoint: string;
let server: ReturnType<typeof startGoFixture> | undefined;

async function buildFixture(packagePath: string): Promise<GoFixtureBinary> {
  const directory = await mkdtemp(join(tmpdir(), "liapoldus-pipe-fixture-"));
  const executable = join(directory, process.platform === "win32" ? "fixture.exe" : "fixture");
  const flags = (process.env.LIAPOLDUS_PEER_FIXTURE_GOFLAGS ?? "").split(/\s+/).filter(Boolean);
  try {
    execFileSync("go", ["build", ...flags, "-o", executable, packagePath], { cwd: root });
  } catch (error) {
    await rm(directory, { recursive: true, force: true });
    throw error;
  }
  return { executable, cleanup: () => rm(directory, { recursive: true, force: true }) };
}

describe("Windows named-pipe carrier contract", () => {
  it("is exposed through the generic public carrier registry", async () => {
    const fixture = await buildFixture(PIPE_FIXTURE);
    try {
      const report = JSON.parse(execFileSync(fixture.executable, ["contract"], { encoding: "utf8" })) as Report;
      expect(report.carrier).toBe("pipe");
    } finally {
      await fixture.cleanup();
    }
  });
});

async function client(args: string[]): Promise<{ report: Report; code: number | null; stderr: string }> {
  const child = startGoFixture(pipeFixture!.executable, {}, args);
  const stderr: string[] = [];
  child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk.toString()));
  const lines = createInterface({ input: child.stdout });
  const report = await new Promise<Report>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("pipe client did not report")), 30_000);
    lines.once("line", (line) => {
      clearTimeout(timeout);
      resolve(JSON.parse(line) as Report);
    });
    child.once("error", reject);
  });
  const code = await new Promise<number | null>((resolve) => {
    if (child.exitCode !== null) resolve(child.exitCode);
    else child.once("exit", resolve);
  });
  return { report, code, stderr: stderr.join("") };
}

async function startFixture(args: string[]): Promise<{ child: ReturnType<typeof startGoFixture>; report: Report }> {
  const child = startGoFixture(pipeFixture!.executable, {}, args);
  const lines = createInterface({ input: child.stdout });
  const report = await new Promise<Report>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error(`${args[0] ?? "fixture"} did not announce readiness`)), 15_000);
    lines.once("line", (line) => {
      clearTimeout(timeout);
      resolve(JSON.parse(line) as Report);
    });
    child.once("error", reject);
  });
  return { child, report };
}

describe.runIf(process.platform === "win32")("Windows named-pipe carrier", () => {
  beforeAll(async () => {
    stateDirectory = await mkdtemp(join(tmpdir(), "liapoldus-peer-pipe-"));
    certificatesDirectory = join(stateDirectory, "certificates");
    await mkdir(certificatesDirectory);
    pipeFixture = await buildFixture(PIPE_FIXTURE);
    certFixture = await buildFixture(PEER_NET_FIXTURE);
    execFileSync(certFixture.executable, ["certs", "--dir", certificatesDirectory], { encoding: "utf8" });
    endpoint = `\\\\.\\pipe\\liapoldus-peer-${process.pid}-${randomUUID()}`;
    server = startGoFixture(pipeFixture.executable, {}, ["server", "--addr", endpoint, "--dir", certificatesDirectory]);
    const lines = createInterface({ input: server.stdout });
    const ready = await new Promise<Report>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("named-pipe server did not announce readiness")), 15_000);
      lines.once("line", (line) => {
        clearTimeout(timeout);
        resolve(JSON.parse(line) as Report);
      });
      server!.once("error", reject);
    });
    expect(ready.ok).toBe(true);
    expect(ready.carrier).toBe("pipe");
    expect(ready.encrypted).toBe(true);
    expect(ready.authenticated).toBe(true);
    endpoint = ready.addr!;
  }, 60_000);

  afterAll(async () => {
    if (server) await stopChildProcess(server);
    if (pipeFixture) await pipeFixture.cleanup();
    if (certFixture) await certFixture.cleanup();
    if (stateDirectory) await rm(stateDirectory, { recursive: true, force: true });
  });

  it("uses mandatory mTLS for unary and bidirectional stream calls across processes", async () => {
    const unary = await client(["client", "--addr", endpoint, "--dir", certificatesDirectory, "--scenario", "unary"]);
    expect(unary.code, `${unary.stderr} ${unary.report.error ?? ""}`).toBe(0);
    expect(unary.report.ok).toBe(true);
    expect(unary.report.carrier).toBe("pipe");
    expect(unary.report.encrypted).toBe(true);
    expect(unary.report.authenticated).toBe(true);

    const stream = await client(["client", "--addr", endpoint, "--dir", certificatesDirectory, "--scenario", "stream"]);
    expect(stream.code, `${stream.stderr} ${stream.report.error ?? ""}`).toBe(0);
    expect(stream.report.ok).toBe(true);
    expect(stream.report.received).toEqual(["a", "b", "c"]);
  }, 60_000);

  it("rejects plaintext and wrong-identity profiles without fallback", async () => {
    const plaintext = await client(["client", "--addr", endpoint, "--dir", certificatesDirectory, "--security", "loopback", "--scenario", "unary"]);
    expect(plaintext.code).toBe(1);
    expect(plaintext.report.ok).toBe(false);

    const wrongIdentity = await client(["client", "--addr", endpoint, "--dir", certificatesDirectory, "--security", "mtls-wrong-identity", "--scenario", "unary"]);
    expect(wrongIdentity.code).toBe(1);
    expect(wrongIdentity.report.ok).toBe(false);
  }, 60_000);

  it("does not permit a second listener to replace an active pipe", async () => {
    const competing = await client(["bind", "--addr", endpoint, "--dir", certificatesDirectory]);
    expect(competing.code).toBe(1);
    expect(competing.report.ok).toBe(false);

    const stillServing = await client(["client", "--addr", endpoint, "--dir", certificatesDirectory, "--scenario", "unary"]);
    expect(stillServing.code).toBe(0);
    expect(stillServing.report.ok).toBe(true);
  }, 60_000);

  it("refuses malformed pipe endpoints with the documented error and creates no pipe", async () => {
    const malformed: Array<{ description: string; addr: string; message: string }> = [
      {
        description: "a nested path separator",
        addr: `\\\\.\\pipe\\liapoldus-peer-nested-${process.pid}${randomUUID()}\\child`,
        message: "pipe: endpoint must identify one named pipe",
      },
      {
        description: "a trailing space",
        addr: `\\\\.\\pipe\\liapoldus-peer-trailing-${process.pid}${randomUUID()} `,
        message: "pipe: endpoint must identify one named pipe",
      },
      {
        description: "an empty pipe name",
        addr: "\\\\.\\pipe\\",
        message: "pipe: endpoint must have form",
      },
    ];

    for (const scenario of malformed) {
      const attempt = await client(["listen", "--addr", scenario.addr, "--dir", certificatesDirectory]);
      expect(attempt.code, `${scenario.description}: ${attempt.stderr} ${attempt.report.message ?? attempt.report.error ?? ""}`).toBe(1);
      expect(attempt.report.ok).toBe(false);
      expect(attempt.report.message).toContain(scenario.message);
      expect(attempt.report.addr).toBeUndefined();

      const probe = await client(["probe", "--addr", scenario.addr]);
      expect(probe.code, `${scenario.description}: ${probe.stderr} ${probe.report.message ?? ""}`).toBe(0);
      expect(probe.report.ok).toBe(true);
      expect(probe.report.exists, `${scenario.description}: a pipe object was created for ${scenario.addr}`).toBe(false);
    }
  }, 60_000);

  it("refuses a pipe listener without a ServerName and creates no pipe", async () => {
    const candidate = `\\\\.\\pipe\\liapoldus-peer-noservername-${process.pid}${randomUUID()}`;
    const attempt = await client(["listen", "--addr", candidate, "--dir", certificatesDirectory, "--server-name", ""]);
    expect(attempt.code, `${attempt.stderr} ${attempt.report.message ?? attempt.report.error ?? ""}`).toBe(1);
    expect(attempt.report.ok).toBe(false);
    expect(attempt.report.message).toContain("pipe: local identity and TLS server name are required");
    expect(attempt.report.addr).toBeUndefined();

    const probe = await client(["probe", "--addr", candidate]);
    expect(probe.code, probe.stderr).toBe(0);
    expect(probe.report.ok).toBe(true);
    expect(probe.report.exists, `a pipe object was created for ${candidate}`).toBe(false);
  }, 60_000);

  it("fails a handshake with a wrong ServerName without dispatching any call", async () => {
    const candidate = `\\\\.\\pipe\\liapoldus-peer-wrongname-${process.pid}${randomUUID()}`;
    const dispatchLog = join(stateDirectory!, `wrong-name-${randomUUID()}.log`);
    const served = await startFixture(["server", "--addr", candidate, "--dir", certificatesDirectory, "--dispatch-log", dispatchLog]);
    try {
      expect(served.report.ok, served.report.error).toBe(true);

      const refused = await client([
        "client",
        "--addr",
        candidate,
        "--dir",
        certificatesDirectory,
        "--server-name",
        "not-the-listener",
        "--scenario",
        "unary",
      ]);
      expect(refused.code, `${refused.stderr} ${refused.report.message ?? ""}`).toBe(1);
      expect(refused.report.ok).toBe(false);
      expect(refused.report.message).toContain("mTLS handshake failed");

      expect(existsSync(dispatchLog) ? readFileSync(dispatchLog, "utf8") : "", "the refused handshake dispatched a call").toBe("");

      const accepted = await client(["client", "--addr", candidate, "--dir", certificatesDirectory, "--scenario", "unary"]);
      expect(accepted.code, `${accepted.stderr} ${accepted.report.message ?? ""}`).toBe(0);
      expect(accepted.report.ok).toBe(true);
      expect(readFileSync(dispatchLog, "utf8")).toContain("fixture.echo");
    } finally {
      await stopChildProcess(served.child);
    }
  }, 120_000);

  it("serves calls again after the pipe server restarts on the same endpoint", async () => {
    const candidate = `\\\\.\\pipe\\liapoldus-peer-restart-${process.pid}${randomUUID()}`;
    const first = await startFixture(["server", "--addr", candidate, "--dir", certificatesDirectory]);
    expect(first.report.ok, first.report.error).toBe(true);

    const before = await client(["client", "--addr", candidate, "--dir", certificatesDirectory, "--scenario", "unary"]);
    expect(before.code, `${before.stderr} ${before.report.message ?? ""}`).toBe(0);
    expect(before.report.ok).toBe(true);

    await stopChildProcess(first.child);

    const replacement = await startFixture(["server", "--addr", candidate, "--dir", certificatesDirectory]);
    try {
      expect(replacement.report.ok, replacement.report.error).toBe(true);
      expect(replacement.report.addr).toBe(candidate);

      const after = await client(["client", "--addr", replacement.report.addr!, "--dir", certificatesDirectory, "--scenario", "unary"]);
      expect(after.code, `${after.stderr} ${after.report.message ?? ""}`).toBe(0);
      expect(after.report.ok).toBe(true);
      expect(after.report.carrier).toBe("pipe");
    } finally {
      await stopChildProcess(replacement.child);
    }
  }, 120_000);

  it("exposes a protected pipe DACL limited to the current user and SYSTEM", async () => {
    const candidate = `\\\\.\\pipe\\liapoldus-peer-dacl-${process.pid}${randomUUID()}`;
    const attempt = await client(["inspect", "--addr", candidate, "--dir", certificatesDirectory]);
    expect(attempt.code, `${attempt.stderr} ${attempt.report.message ?? attempt.report.error ?? ""}`).toBe(0);
    expect(attempt.report.ok, attempt.report.message).toBe(true);
    expect(attempt.report.daclRestricted, attempt.report.message).toBe(true);
  }, 60_000);
});
