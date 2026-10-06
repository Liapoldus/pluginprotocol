import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { lstat, mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:net";
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
  carrier?: string;
  encrypted?: boolean;
  authenticated?: boolean;
  admitted?: boolean;
  reason?: string;
}

interface Outcome {
  report: FixtureReport;
  code: number | null;
  stderr: string;
  elapsedMs: number;
}

let fixture: GoFixtureBinary | undefined;
let temporaryDirectory: string | undefined;
let certificatesDirectory: string;
let probeSocketPath: string;
let probeServer: ReturnType<typeof startGoFixture> | undefined;

// runFixture starts the fixture once, waits for its single report and collects the
// exit code together with how long the child took, because a local refusal that
// takes seconds is as much a failure as one that never happens at all. A failing
// command is a normal outcome for the negative cases, so it is reported rather than
// thrown.
async function runFixture(args: string[], timeoutMs = 30_000): Promise<Outcome> {
  const started = Date.now();
  const child = startGoFixture(fixture!.executable, {}, args);
  const stderr: string[] = [];
  child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk.toString()));
  const lines = createInterface({ input: child.stdout });
  const exited = new Promise<number | null>((resolve) => {
    if (child.exitCode !== null) {
      resolve(child.exitCode);
      return;
    }
    child.once("exit", (value) => resolve(value));
  });
  const report = await new Promise<FixtureReport>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${args.join(" ")} did not report`)), timeoutMs);
    lines.once("line", (line) => {
      clearTimeout(timer);
      resolve(JSON.parse(line) as FixtureReport);
    });
    child.once("error", reject);
  });
  const code = await exited;
  return { report, code, stderr: stderr.join(""), elapsedMs: Date.now() - started };
}

// startServer starts a fixture endpoint that keeps serving and resolves once it has
// announced its address, which is how a test gets a live listener to probe.
async function startServer(args: string[]): Promise<{ child: ReturnType<typeof startGoFixture>; report: FixtureReport }> {
  const child = startGoFixture(fixture!.executable, {}, args);
  const lines = createInterface({ input: child.stdout });
  const report = await new Promise<FixtureReport>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${args.join(" ")} did not announce readiness`)), 15_000);
    lines.once("line", (line) => {
      clearTimeout(timer);
      resolve(JSON.parse(line) as FixtureReport);
    });
    child.once("error", reject);
  });
  return { child, report };
}

function serverArgs(socketPath: string, extra: string[] = []): string[] {
  return [
    "server",
    "--carrier",
    "unix",
    "--security",
    "mtls",
    "--dir",
    certificatesDirectory,
    "--addr",
    `unix://${socketPath}`,
    ...extra,
  ];
}

function clientArgs(socketPath: string, extra: string[] = []): string[] {
  return [
    "client",
    "--carrier",
    "unix",
    "--security",
    "mtls",
    "--dir",
    certificatesDirectory,
    "--addr",
    `unix://${socketPath}`,
    "--scenario",
    "unary",
    ...extra,
  ];
}

// freePort reserves an ephemeral loopback port and releases it again, so a test can
// demand that nothing is left listening on that exact port afterwards.
async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const probe = createServer();
    probe.once("error", reject);
    probe.listen(0, "127.0.0.1", () => {
      const address = probe.address();
      if (address === null || typeof address === "string") {
        probe.close(() => reject(new Error("no ephemeral port")));
        return;
      }
      const port = address.port;
      probe.close(() => resolve(port));
    });
  });
}

async function portIsFree(port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const probe = createServer();
    probe.once("error", () => resolve(false));
    probe.listen(port, "127.0.0.1", () => probe.close(() => resolve(true)));
  });
}

describe.skipIf(platform() === "win32")("Unix carrier guards", () => {
  beforeAll(async () => {
    fixture = await buildGoFixture(root, FIXTURE_PACKAGE);
    temporaryDirectory = await mkdtemp(join(tmpdir(), "liapoldus-peer-unix-guards-"));
    certificatesDirectory = join(temporaryDirectory, "certificates");
    execFileSync(fixture.executable, ["certs", "--dir", certificatesDirectory], { encoding: "utf8" });

    probeSocketPath = join(temporaryDirectory, "probe.sock");
    const started = await startServer(serverArgs(probeSocketPath));
    expect(started.report.ok, started.report.error).toBe(true);
    expect(started.report.carrier).toBe("unix");
    probeServer = started.child;
  }, 180_000);

  afterAll(async () => {
    if (probeServer) await stopChildProcess(probeServer);
    if (fixture) await fixture.cleanup();
    if (temporaryDirectory) await rm(temporaryDirectory, { recursive: true, force: true });
  }, 30_000);

  it("fails a dial to a missing socket locally instead of falling back to TCP", async () => {
    const missing = join(temporaryDirectory!, "missing.sock");
    const attempt = await runFixture(clientArgs(missing));
    expect(attempt.code, `${attempt.stderr} ${attempt.report.error ?? ""}`).toBe(1);
    expect(attempt.report.ok).toBe(false);
    const reported = `${attempt.report.error ?? ""}${attempt.stderr}`;
    expect(reported).toContain("unix: dial failed");
    // A TCP fallback would surface as a refused connection to a host:port, never as
    // a filesystem error naming the socket path that was asked for.
    expect(reported).not.toMatch(/connection refused/i);
    expect(reported).not.toMatch(/dial tcp/i);
    // The refusal is local, so it cannot wait out a dial timeout.
    expect(attempt.elapsedMs).toBeLessThan(15_000);
    expect(existsSync(missing)).toBe(false);
  }, 60_000);

  it("refuses a TCP endpoint on the unix carrier without binding a TCP listener", async () => {
    const port = await freePort();
    const mismatched = await runFixture([
      "server",
      "--carrier",
      "unix",
      "--security",
      "mtls",
      "--dir",
      certificatesDirectory,
      "--addr",
      `tcp://127.0.0.1:${port}`,
    ]);
    expect(mismatched.code, `${mismatched.stderr} ${mismatched.report.error ?? ""}`).toBe(1);
    expect(mismatched.report.ok).toBe(false);
    expect(mismatched.report.error).toContain("unix: endpoint must be unix:///absolute/path");
    expect(mismatched.report.addr).toBeUndefined();
    expect(await portIsFree(port), "the refused listen left a TCP listener bound").toBe(true);
  }, 60_000);

  it("refuses a non-absolute unix endpoint without creating anything", async () => {
    const attempt = await runFixture([
      "server",
      "--carrier",
      "unix",
      "--security",
      "mtls",
      "--dir",
      certificatesDirectory,
      "--addr",
      "unix://relative/path.sock",
    ]);
    expect(attempt.code, `${attempt.stderr} ${attempt.report.error ?? ""}`).toBe(1);
    expect(attempt.report.ok).toBe(false);
    expect(attempt.report.error).toContain("unix: endpoint must be unix:///absolute/path");
    expect(attempt.report.addr).toBeUndefined();
    expect(existsSync(join(process.cwd(), "path.sock"))).toBe(false);
    expect(existsSync(join(process.cwd(), "relative"))).toBe(false);
    expect(existsSync(join(root, "path.sock"))).toBe(false);
  }, 60_000);

  it("refuses a unix endpoint on the tcp carrier without creating the socket", async () => {
    const socketPath = join(temporaryDirectory!, "inverse.sock");
    const attempt = await runFixture([
      "server",
      "--carrier",
      "tcp",
      "--security",
      "mtls",
      "--dir",
      certificatesDirectory,
      "--addr",
      `unix://${socketPath}`,
    ]);
    expect(attempt.code, `${attempt.stderr} ${attempt.report.error ?? ""}`).toBe(1);
    expect(attempt.report.ok).toBe(false);
    expect(attempt.report.error).toContain("tcp");
    expect(existsSync(socketPath), "the tcp carrier created a unix socket").toBe(false);
  }, 60_000);

  it.each([
    { name: "raw plaintext bytes", mode: "plaintext" },
    { name: "TLS with no client certificate", mode: "no-certificate" },
    { name: "a certificate chained to an unknown anchor", mode: "untrusted-client" },
  ])("refuses $name before dispatch and keeps serving afterwards", async ({ mode }) => {
    const probe = await runFixture(["tls", "--addr", `unix://${probeSocketPath}`, "--dir", certificatesDirectory, "--mode", mode]);
    expect(probe.code, `${probe.stderr} ${probe.report.error ?? ""}`).toBe(0);
    expect(probe.report.ok, probe.report.error).toBe(true);
    expect(probe.report.admitted).toBe(false);
  }, 60_000);

  it("keeps serving ordinary calls after the unauthenticated probes", async () => {
    const after = await runFixture(clientArgs(probeSocketPath));
    expect(after.code, `${after.stderr} ${after.report.error ?? ""}`).toBe(0);
    expect(after.report.ok, after.report.error).toBe(true);
    expect(after.report.scenario).toBe("unary");
  }, 60_000);

  it("refuses a unix listen with no ServerName and creates no socket", async () => {
    const socketPath = join(temporaryDirectory!, "no-server-name.sock");
    const attempt = await runFixture([
      "facade",
      "--carrier",
      "unix",
      "--security",
      "mtls",
      "--dir",
      certificatesDirectory,
      "--endpoint",
      `unix://${socketPath}`,
      "--empty-server-name",
    ]);
    expect(attempt.code, `${attempt.stderr} ${attempt.report.error ?? ""}`).toBe(1);
    expect(attempt.report.ok).toBe(false);
    expect(attempt.report.error).toContain("unix: a TLS server name is required");
    expect(existsSync(socketPath), "the refused listen still created a socket").toBe(false);
  }, 60_000);

  it("fails a wrong ServerName handshake without dispatching a call", async () => {
    const socketPath = join(temporaryDirectory!, "wrong-name.sock");
    const dispatchLog = join(temporaryDirectory!, "wrong-name-dispatch.log");
    const served = await startServer(serverArgs(socketPath, ["--dispatch-log", dispatchLog]));
    try {
      expect(served.report.ok, served.report.error).toBe(true);

      const attempt = await runFixture(
        clientArgs(socketPath, ["--server-name", "not-the-listener"]),
      );
      expect(attempt.code, `${attempt.stderr} ${attempt.report.error ?? ""}`).toBe(1);
      expect(attempt.report.ok).toBe(false);
      expect(attempt.report.error).toContain("not-the-listener");

      const dispatchedBefore = existsSync(dispatchLog) ? await readFile(dispatchLog, "utf8") : "";
      expect(dispatchedBefore, "the refused handshake still dispatched a call").toBe("");

      const accepted = await runFixture(clientArgs(socketPath));
      expect(accepted.code, `${accepted.stderr} ${accepted.report.error ?? ""}`).toBe(0);
      expect(accepted.report.ok, accepted.report.error).toBe(true);
      expect(await readFile(dispatchLog, "utf8")).toContain("example.echo");
    } finally {
      await stopChildProcess(served.child);
    }
  }, 120_000);

  it("serves a fresh call after a restart on the same socket path", async () => {
    const socketPath = join(temporaryDirectory!, "restart.sock");

    const first = await startServer(serverArgs(socketPath));
    expect(first.report.ok, first.report.error).toBe(true);
    const before = await runFixture(clientArgs(socketPath));
    expect(before.code, `${before.stderr} ${before.report.error ?? ""}`).toBe(0);
    expect(before.report.ok, before.report.error).toBe(true);

    first.child.kill("SIGKILL");
    await new Promise<void>((resolve) => first.child.once("exit", () => resolve()));

    const replacement = await startServer(serverArgs(socketPath));
    try {
      expect(replacement.report.ok, replacement.report.error).toBe(true);
      const after = await runFixture(clientArgs(socketPath));
      expect(after.code, `${after.stderr} ${after.report.error ?? ""}`).toBe(0);
      expect(after.report.ok, after.report.error).toBe(true);

      const info = await lstat(socketPath);
      expect(info.isSocket()).toBe(true);
      expect(info.mode & 0o777).toBe(0o600);
    } finally {
      await stopChildProcess(replacement.child);
    }
  }, 120_000);

  // The probe cases above prove the handshake is refused. This proves the refusal
  // happens before any handler runs, which is the part a refusal-only assertion
  // cannot show: the log stays empty for every rejected probe, and an admitted call
  // on the same endpoint then fills it, so an empty log is evidence rather than an
  // unobserved signal.
  it("refuses every unauthenticated probe before any handler dispatch", async () => {
    const socketPath = join(temporaryDirectory!, "probe-dispatch.sock");
    const dispatchLog = join(temporaryDirectory!, "probe-dispatch.log");
    const served = await startServer(serverArgs(socketPath, ["--dispatch-log", dispatchLog]));
    try {
      expect(served.report.ok, served.report.error).toBe(true);

      for (const mode of ["plaintext", "no-certificate", "untrusted-client"]) {
        const probe = await runFixture([
          "tls",
          "--addr",
          `unix://${socketPath}`,
          "--dir",
          certificatesDirectory,
          "--mode",
          mode,
        ]);
        expect(probe.code, `${probe.stderr} ${probe.report.error ?? ""}`).toBe(0);
        expect(probe.report.ok, probe.report.error).toBe(true);
        expect(probe.report.admitted, `${mode} was admitted`).toBe(false);
        const dispatched = existsSync(dispatchLog) ? await readFile(dispatchLog, "utf8") : "";
        expect(dispatched, `${mode} reached a handler`).toBe("");
      }

      const accepted = await runFixture(clientArgs(socketPath));
      expect(accepted.code, `${accepted.stderr} ${accepted.report.error ?? ""}`).toBe(0);
      expect(accepted.report.ok, accepted.report.error).toBe(true);
      expect(await readFile(dispatchLog, "utf8")).toContain("example.echo");
    } finally {
      await stopChildProcess(served.child);
    }
  }, 120_000);

  // The scheme guards above cover a refused listen. A refused dial has to be shown
  // too, because a fallback would happen on the dialling side: the endpoint is
  // rejected before any connection is attempted, so no TCP port is ever reached and
  // no relative socket appears on disk.
  it("refuses a mismatched endpoint scheme on a unix dial without touching anything", async () => {
    const port = await freePort();
    const mismatched = await runFixture([
      "client",
      "--carrier",
      "unix",
      "--security",
      "mtls",
      "--dir",
      certificatesDirectory,
      "--addr",
      `tcp://127.0.0.1:${port}`,
      "--scenario",
      "unary",
    ]);
    expect(mismatched.code, `${mismatched.stderr} ${mismatched.report.error ?? ""}`).toBe(1);
    expect(mismatched.report.ok).toBe(false);
    expect(mismatched.report.error).toContain("unix: endpoint must be unix:///absolute/path");
    expect(mismatched.elapsedMs).toBeLessThan(15_000);
    expect(await portIsFree(port), "the refused dial reached a TCP endpoint").toBe(true);

    const relative = await runFixture([
      "client",
      "--carrier",
      "unix",
      "--security",
      "mtls",
      "--dir",
      certificatesDirectory,
      "--addr",
      "unix://relative/path.sock",
      "--scenario",
      "unary",
    ]);
    expect(relative.code, `${relative.stderr} ${relative.report.error ?? ""}`).toBe(1);
    expect(relative.report.ok).toBe(false);
    expect(relative.report.error).toContain("unix: endpoint must be unix:///absolute/path");
    expect(existsSync(join(process.cwd(), "relative"))).toBe(false);
    expect(existsSync(join(root, "path.sock"))).toBe(false);
  }, 60_000);
});
