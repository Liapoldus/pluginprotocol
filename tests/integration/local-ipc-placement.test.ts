import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { type ChildProcessWithoutNullStreams } from "node:child_process";
import { randomUUID } from "node:crypto";
import { createInterface } from "node:readline";
import { rm, mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
const FIXTURE_PACKAGE = "./tests/fixtures/peer-net";

const profile = "mtls";
const serverIdentity = "spiffe://liapoldus/dev/peer-net-server";
const clientIdentity = "spiffe://liapoldus/dev/peer-net-client";

interface Report {
  ok: boolean;
  error?: string;
  role?: string;
  addr?: string;
  dialed?: string;
  carrier?: string;
  securityProfile?: string;
  encrypted?: boolean;
  authenticated?: boolean;
  echo?: string;
  upper?: string;
  caller_identity?: string;
  received?: string[];
  outboundStreams?: number;
  localIdentity?: string;
  peerIdentity?: string;
}

let fixture: GoFixtureBinary | undefined;
let executable: string;
let certificates: string;

async function runFixture(args: string[]): Promise<{ report: Report; stderr: string }> {
  const child: ChildProcessWithoutNullStreams = startGoFixture(executable, {}, args);
  const lines = createInterface({ input: child.stdout });
  const stderr: string[] = [];
  child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk.toString()));
  // Attach the exit listener before waiting for stdout. Fast Linux child processes
  // can print their report and exit before the report is consumed; registering
  // `once("exit")` afterwards would then wait forever.
  const exited = new Promise<number | null>((resolve) => {
    if (child.exitCode !== null) {
      resolve(child.exitCode);
      return;
    }
    child.once("exit", (value) => resolve(value));
  });
  const reported = new Promise<Report>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${args.join(" ")} did not report`)), 60_000);
    lines.once("line", (line) => {
      clearTimeout(timer);
      resolve(JSON.parse(line) as Report);
    });
  });
  const report = await reported;
  const code = await exited;
  if (!report.ok && code !== 1) {
    throw new Error(`${args.join(" ")} exited with ${code}: ${stderr.join("")}`);
  }
  return { report, stderr: stderr.join("") };
}

class FixtureProcess {
  private constructor(
    private readonly child: ChildProcessWithoutNullStreams,
    private readonly lines: ReturnType<typeof createInterface>,
    readonly report: Report,
    readonly address: string,
  ) {}

  static async start(args: string[]): Promise<FixtureProcess> {
    const child: ChildProcessWithoutNullStreams = startGoFixture(executable, {}, args);
    const lines = createInterface({ input: child.stdout });
    const announced = new Promise<Report>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`${args.join(" ")} did not announce an address`)), 15_000);
      lines.once("line", (line) => {
        clearTimeout(timer);
        resolve(JSON.parse(line) as Report);
      });
    });
    const report = await announced;
    expect(report.ok).toBe(true);
    if (report.addr === undefined) throw new Error(`${args.join(" ")} announced no address`);
    return new FixtureProcess(child, lines, report, report.addr);
  }

  // nextReport waits for the following JSON line, which is how the hub's second
  // report (the outbound dialog) is consumed after the announce.
  async nextReport(): Promise<Report> {
    return new Promise<Report>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("fixture did not report its next line")), 30_000);
      this.lines.once("line", (line) => {
        clearTimeout(timer);
        resolve(JSON.parse(line) as Report);
      });
    });
  }

  async stop(): Promise<void> {
    await stopChildProcess(this.child);
    this.lines.close();
  }
}

// assertMixedPlacement proves one operating-system process can be an endpoint and
// a caller at the same time over a local carrier:
//
//   peer (server) <--dial-- hub <--dial-- client
//
// The hub announces its own address, dials the peer, keeps serving, and then has
// to admit the client while it still owns the outbound session. The authenticated
// identities assert both roles held the credentials their profile requires.
async function assertMixedPlacement(carrier: string, peerAddress: string, hubAddress: string): Promise<void> {
  let peer: FixtureProcess | undefined;
  let hub: FixtureProcess | undefined;
  try {
    peer = await FixtureProcess.start(["server", "--carrier", carrier, "--security", profile, "--dir", certificates, "--addr", peerAddress]);
    hub = await FixtureProcess.start(["hub", "--carrier", carrier, "--security", profile, "--dir", certificates, "--addr", hubAddress, "--peer", peerAddress]);

    expect(hub.report.role).toBe("hub");
    expect(hub.report.carrier).toBe(carrier);
    expect(hub.report.securityProfile).toBe("mtls");
    expect(hub.report.encrypted).toBe(true);
    expect(hub.report.authenticated).toBe(true);

    const dial = await hub.nextReport();
    expect(dial.ok, dial.error).toBe(true);
    expect(dial.role).toBe("hub-dial");
    expect(dial.dialed).toBe(peerAddress);
    expect(dial.carrier).toBe(carrier);
    expect(dial.securityProfile).toBe("mtls");
    expect(dial.echo).toBe("hub");
    expect(dial.upper).toBe("HUB");
    expect(dial.received).toEqual(["x", "y"]);
    expect(dial.localIdentity).toBe(clientIdentity);
    expect(dial.peerIdentity).toBe(serverIdentity);
    expect(dial.outboundStreams).toBe(2);

    // The hub must keep its outbound streams open while it serves the inbound
    // caller. They consume the peer's complete stream budget, so another caller
    // must be refused rather than succeeding after the hub has closed its session.
    const peerProbe = await runFixture([
      "client",
      "--carrier",
      carrier,
      "--addr",
      peerAddress,
      "--security",
      profile,
      "--dir",
      certificates,
      "--scenario",
      "probe-stream",
    ]);
    expect(peerProbe.report.ok).toBe(false);
    expect(peerProbe.report.error).toContain("concurrency limit exceeded");

    // The client dials the hub after the hub has already dialed its peer, so the
    // placement under test is the serving half of a real hub, not a process that
    // dialed and then quietly stopped listening.
    const client = await runFixture(["client", "--carrier", carrier, "--addr", hub.address, "--security", profile, "--dir", certificates, "--scenario", "identity"]);
    expect(client.stderr).toBe("");
    expect(client.report.ok).toBe(true);
    expect(client.report.echo).toBe("hello");
    expect(client.report.localIdentity).toBe(clientIdentity);
    expect(client.report.peerIdentity).toBe(serverIdentity);

    // The client verifying the hub's serving certificate only proves the inbound
    // client side. Ask the hub's handler who it authenticated so this mixed-role
    // process also proves the reverse identity direction while its outbound
    // session to peer remains open.
    const inboundIdentity = await runFixture([
      "client",
      "--carrier",
      carrier,
      "--addr",
      hub.address,
      "--security",
      profile,
      "--dir",
      certificates,
      "--scenario",
      "unary",
    ]);
    expect(inboundIdentity.report.ok).toBe(true);
    expect(inboundIdentity.report.caller_identity).toBe(clientIdentity);
  } finally {
    await hub?.stop();
    await peer?.stop();
  }
}

beforeAll(async () => {
  fixture = await buildGoFixture(root, FIXTURE_PACKAGE);
  executable = fixture.executable;
  certificates = await mkdtemp(join(tmpdir(), "liapoldus-peer-certs-"));
  const { report } = await runFixture(["certs", "--dir", certificates]);
  expect(report.ok).toBe(true);
}, 180_000);

afterAll(async () => {
  if (certificates !== undefined) await rm(certificates, { recursive: true, force: true });
  await fixture?.cleanup();
}, 30_000);

describe.skipIf(process.platform === "win32")("mixed-placement hosts over the unix carrier", () => {
  it("serves inbound sessions while holding an outbound one", async () => {
    const socketDirectory = await mkdtemp(join(tmpdir(), "liapoldus-peer-placement-"));
    try {
      await assertMixedPlacement("unix", `unix://${join(socketDirectory, "peer.sock")}`, `unix://${join(socketDirectory, "hub.sock")}`);
    } finally {
      await rm(socketDirectory, { recursive: true, force: true });
    }
  }, 90_000);
});

describe.runIf(process.platform === "win32")("mixed-placement hosts over the named-pipe carrier", () => {
  it("serves inbound sessions while holding an outbound one", async () => {
    const unique = `${process.pid}-${randomUUID()}`;
    await assertMixedPlacement("pipe", `\\\\.\\pipe\\liapoldus-peer-mixed-${unique}-peer`, `\\\\.\\pipe\\liapoldus-peer-mixed-${unique}-hub`);
  }, 90_000);
});
