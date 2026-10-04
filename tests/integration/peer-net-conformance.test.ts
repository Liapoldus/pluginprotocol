import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
const FIXTURE_PACKAGE = "./tests/fixtures/peer-net";

interface Report {
  ok: boolean;
  error?: string;
  scenario?: string;
  addr?: string;
  role?: string;
  mode?: string;
  admitted?: boolean;
  reason?: string;
  carrier?: string;
  securityProfile?: string;
  encrypted?: boolean;
  authenticated?: boolean;
  localIdentity?: string;
  peerIdentity?: string;
  echo?: string;
  upper?: string;
  caller_identity?: string;
  request_too_large?: boolean;
  response_too_large?: boolean;
  handler_panic?: boolean;
  stream_panic?: boolean;
  received?: string[];
  accepted?: number;
  canceled?: boolean;
  overloaded?: boolean;
  default_message_bytes?: number;
  accepted_bytes?: number;
  open?: number;
  holding?: boolean;
  deadline?: boolean;
  callDenied?: boolean;
  streamDenied?: boolean;
  iterations?: number;
  terminated?: number;
  sessions?: number;
  concurrency?: number;
  failures?: number;
  goroutinesBefore?: number;
  goroutinesAfter?: number;
}

interface Outcome {
  report: Report;
  code: number | null;
  stderr: string;
}

// The carrier and security combinations a deployment can select.
//
// Every behavioural scenario runs against all of them, because selecting a carrier
// or a profile must change how bytes travel and nothing else. An endpoint that
// behaves differently once encryption or a different carrier is on has not been
// made safe, it has been made differently configured, and only running the same
// assertions over each combination can show that.
const COMBINATIONS = [
  { name: "tcp/loopback", carrier: "tcp", profile: "loopback", profileName: "loopback-plaintext", encrypted: false, authenticated: false },
  { name: "tcp/mtls", carrier: "tcp", profile: "mtls", profileName: "mtls", encrypted: true, authenticated: true },
  { name: "quic/mtls", carrier: "quic", profile: "mtls", profileName: "mtls", encrypted: true, authenticated: true },
] as const;

const SERVER_IDENTITY = "spiffe://liapoldus/dev/peer-net-server";
const CLIENT_IDENTITY = "spiffe://liapoldus/dev/peer-net-client";

let fixture: GoFixtureBinary;
let executable: string;
let certificates: string;

// runFixture starts the fixture once, waits for its single report and collects the
// exit code. A failing command is a normal outcome for the negative cases, so it is
// reported rather than thrown.
async function runFixture(args: string[], timeout = 60_000): Promise<Outcome> {
  const child: ChildProcessWithoutNullStreams = startGoFixture(executable, {}, args);
  const lines = createInterface({ input: child.stdout });
  const stderr: string[] = [];
  child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk.toString()));
  // Attach the exit listener before waiting for stdout. Fast Linux child
  // processes can print their report and exit before the report is consumed;
  // registering `once("exit")` afterwards would then wait forever.
  const exited = new Promise<number | null>((resolve) => {
    if (child.exitCode !== null) {
      resolve(child.exitCode);
      return;
    }
    child.once("exit", (value) => resolve(value));
  });

  const reported = new Promise<Report>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${args.join(" ")} did not report`)), timeout);
    lines.once("line", (line) => {
      clearTimeout(timer);
      resolve(JSON.parse(line) as Report);
    });
  });
  const report = await reported;
  const code = await exited;
  // A scenario either succeeds or explains itself. A bare failure with no report is
  // a defect, so the exit code and stderr are part of the failure text.
  if (!report.ok && code !== 1) {
    throw new Error(`${args.join(" ")} exited with ${code}: ${stderr.join("")}`);
  }
  return { report, code, stderr: stderr.join("") };
}

// PeerNetServer is one fixture endpoint in its own operating-system process, so
// every assertion below covers the framed wire protocol across a process boundary
// rather than through an in-process shortcut.
class PeerNetServer {
  private constructor(
    private readonly child: ChildProcessWithoutNullStreams,
    readonly address: string,
    readonly report: Report,
  ) {}

  static async start(carrier: string, profile: string, extraArgs: string[] = []): Promise<PeerNetServer> {
    const child = startGoFixture(executable, {}, ["server", "--carrier", carrier, "--security", profile, "--dir", certificates, ...extraArgs]);
    const lines = createInterface({ input: child.stdout });
    const announced = new Promise<Report>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("server did not announce an address")), 15_000);
      lines.once("line", (line) => {
        clearTimeout(timer);
        resolve(JSON.parse(line) as Report);
      });
    });
    const report = await announced;
    expect(report.ok).toBe(true);
    if (report.addr === undefined) throw new Error("server announced no address");
    return new PeerNetServer(child, report.addr, report);
  }

  async stop(): Promise<void> {
    await stopChildProcess(this.child);
  }
}

// startHoldingClient starts a client that keeps its streams open and its process
// alive, and resolves once the fixture reports what the serving peer admitted. It
// exists so a test can observe the serving budget while a peer is deliberately
// still connected.
async function startHoldingClient(args: string[]): Promise<{ child: ChildProcessWithoutNullStreams; report: Report }> {
  const child: ChildProcessWithoutNullStreams = startGoFixture(executable, {}, args);
  const lines = createInterface({ input: child.stdout });
  const report = await new Promise<Report>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${args.join(" ")} did not report`)), 60_000);
    lines.once("line", (line) => {
      clearTimeout(timer);
      resolve(JSON.parse(line) as Report);
    });
  });
  return { child, report };
}

// budgetIsFull reports whether the serving peer still refuses a fresh stream, which
// is how a test can tell that the bounded budget is fully committed. The probe opens
// a single stream, so asking the question does not fill the budget being asked about.
async function budgetIsFull(server: PeerNetServer, combination: (typeof COMBINATIONS)[number]): Promise<boolean> {
  const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "probe-stream"));
  return !report.ok;
}

async function waitForFreeBudget(
  server: PeerNetServer,
  combination: (typeof COMBINATIONS)[number],
  timeoutMs: number,
): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (!(await budgetIsFull(server, combination))) return true;
    if (Date.now() > deadline) return false;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
}

// expectFreeBudget waits for the shared endpoint to have capacity again. The budget
// is bounded per serving router and shared by every peer, so a scenario that
// deliberately leaves a stream open still owns capacity for a moment after its own
// process exits. Asserting the budget on top of that would make the result depend on
// how quickly the serving peer reaped the previous peer instead of on the behaviour
// under test. The patience is generous because the budget is released by the serving
// peer after it observes the previous connection close, which under the race detector
// can take noticeably longer than the work that caused it.
async function expectFreeBudget(server: PeerNetServer, combination: (typeof COMBINATIONS)[number]): Promise<void> {
  expect(
    await waitForFreeBudget(server, combination, 30_000),
    "the shared endpoint never returned a stream slot",
  ).toBe(true);
}

beforeAll(async () => {
  fixture = await buildGoFixture(root, FIXTURE_PACKAGE);
  executable = fixture.executable;
  certificates = await mkdtemp(join(tmpdir(), "liapoldus-peer-certs-"));
  await runFixture(["certs", "--dir", certificates]);
}, 180_000);

afterAll(async () => {
  if (certificates !== undefined) await rm(certificates, { recursive: true, force: true });
  await fixture?.cleanup();
}, 30_000);

describe.each(COMBINATIONS)("carrier conformance over $name", (combination) => {
  let server: PeerNetServer;

  beforeAll(async () => {
    server = await PeerNetServer.start(combination.carrier, combination.profile);
  }, 60_000);

  afterAll(async () => {
    await server?.stop();
  }, 30_000);

  it("reports the carrier and the profile it actually used", () => {
    expect(server.report.carrier).toBe(combination.carrier);
    expect(server.report.securityProfile).toBe(combination.profileName);
    expect(server.report.encrypted).toBe(combination.encrypted);
    expect(server.report.authenticated).toBe(combination.authenticated);
  });

  it("recycles sessions without leaking goroutines", async () => {
    // A single-session scenario cannot see a per-session leak, because the process
    // exits straight afterwards. Cycling many sessions and demanding that the
    // client return to its baseline goroutine count is what makes an accumulating
    // read loop, probe loop or deadline timer observable.
    const { report, code, stderr } = await runFixture([
      "soak",
      "--carrier",
      combination.carrier,
      "--addr",
      server.address,
      "--security",
      combination.profile,
      "--dir",
      certificates,
      "--sessions",
      "40",
      "--concurrency",
      "4",
    ]);
    expect(stderr).toBe("");
    expect(code).toBe(0);
    expect(report.ok).toBe(true);
    expect(report.failures).toBe(0);
    expect(report.sessions).toBe(40);
    expect(report.goroutinesBefore).toBeGreaterThan(0);
    expect(report.goroutinesAfter).toBeLessThanOrEqual(report.goroutinesBefore! + 16);
  });

  it("fails an in-flight call when the session is torn down, on every carrier", async () => {
    const { report, code, stderr } = await runFixture([
      "wire",
      "--carrier",
      combination.carrier,
      "--addr",
      server.address,
      "--case",
      "close-race",
      "--security",
      combination.profile,
      "--dir",
      certificates,
    ]);
    expect(code).toBe(0);
    expect(report.ok).toBe(true);
    expect(report.callFailedErr).toBeTruthy();
    expect(report.callFailedErr).not.toContain("deadline");
  });

  it("carries request and response payloads in both directions", async () => {
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "unary"));
    expect(report.ok).toBe(true);
    expect(report.echo).toBe("hello");
    expect(report.upper).toBe("HELLO");
  });

  it("tells a handler only the identity the carrier authenticated", async () => {
    // The remote identity is a property of the verified session, not something a
    // request can state, so this is asserted on every combination: an encrypted one
    // names the peer the certificate proved, and a loopback development profile
    // reports no identity rather than an unproven one.
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "unary"));
    expect(report.ok).toBe(true);
    expect(report.caller_identity).toBe(combination.authenticated ? "spiffe://liapoldus/dev/peer-net-client" : "");
  });

  it("refuses an oversized request and an oversized response", async () => {
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "unary"));
    expect(report.request_too_large).toBe(true);
    expect(report.response_too_large).toBe(true);
  });

  it("applies default limits when a consumer sets none", async () => {
    // A consumer is documented to get DefaultLimits for every field it leaves unset.
    // That fallback is load-bearing rather than cosmetic: a zero MaxMessageBytes
    // would reject every payload and a zero MaxConcurrentCalls would reject every
    // call, so the resolved budget is observed from a peer that set no limits at all
    // instead of from the source. The endpoint is dedicated for the same reason the
    // concurrency tests use one: it has to serve with no configured budget, which the
    // shared endpoint does not.
    const dedicated = await PeerNetServer.start(combination.carrier, combination.profile, ["--limits", "default"]);
    try {
      const { report } = await runFixture(clientArgs(dedicated, combination.carrier, combination.profile, "default-limits"));
      expect(report.ok).toBe(true);
      // Larger than the fixture's own 1 KiB bound, so a registry that kept the
      // fixture limits or failed to resolve them cannot pass this.
      expect(report.accepted_bytes).toBe(64 * 1024);
      // And the documented bound is finite and larger than the fixture's own, so
      // "unset" resolves to a real budget rather than to zero or to no limit at all.
      // That the resolved bound is enforced on the wire is asserted where it costs
      // kilobytes: the hostile-framing scenario refuses requests and responses one
      // byte past the configured bound on every carrier.
      expect(report.default_message_bytes).toBeGreaterThan(64 * 1024);
      expect(report.default_message_bytes).toBeLessThanOrEqual(16 * 1024 * 1024);
    } finally {
      await dedicated.stop();
    }
  });

  it("releases every session it opens, so repeated calls cannot leak", async () => {
    // Each other scenario opens one session and exits, so a read loop, probe loop or
    // deadline timer left behind by Close would stay under the noise floor. Cycling
    // many sessions accumulates it into a visible difference, which is what makes the
    // close path worth asserting separately from the behaviour that precedes it.
    //
    // A dedicated endpoint, because the probe opens far more sessions than the
    // fixture's own bound is sized for: on the shared one it would consume the stream
    // budget the next scenario asserts on, and the failure would land somewhere
    // unrelated.
    const dedicated = await PeerNetServer.start(combination.carrier, combination.profile);
    try {
      const { report, code } = await runFixture(soakArgs(dedicated, combination.carrier, combination.profile, 40, 4), 180_000);
      expect(report.ok, report.error).toBe(true);
      expect(code).toBe(0);
      expect(report.failures).toBe(0);
      expect(report.sessions).toBe(40);
      expect(report.concurrency).toBe(4);
      // The probe samples until the count settles, then compares against the baseline
      // it took after a warmup session. One leaked goroutine per session would put the
      // final count far above this, so the slack is noise tolerance, not headroom.
      expect(report.goroutinesBefore).toBeGreaterThan(0);
      expect(report.goroutinesAfter ?? 0).toBeLessThanOrEqual((report.goroutinesBefore ?? 0) + 16);
    } finally {
      await dedicated.stop();
    }
  }, 240_000);

  it("isolates a panicking handler instead of dropping the session", async () => {
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "unary"));
    expect(report.ok).toBe(true);
    expect(report.handler_panic).toBe(true);
  });

  it("reports an unknown method without falling back to another handler", async () => {
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "unary"));
    expect(report.ok).toBe(true);
  });

  it("propagates cancellation to the handler and back to the caller", async () => {
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "cancel-call"));
    expect(report.ok).toBe(true);
    expect(report.canceled).toBe(true);
  });

  it("propagates a caller deadline instead of running to completion", async () => {
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "deadline"));
    expect(report.ok).toBe(true);
    expect(report.deadline).toBe(true);
  });

  it("exchanges stream messages in both directions", async () => {
    await expectFreeBudget(server, combination);
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "stream"));
    expect(report.received).toEqual(["a", "b", "c"]);
  });

  it("isolates a panicking stream handler instead of dropping the session", async () => {
    await expectFreeBudget(server, combination);
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "stream"));
    expect(report.ok).toBe(true);
    expect(report.stream_panic).toBe(true);
  });

  it("refuses a bounded send queue rather than buffering without limit", async () => {
    await expectFreeBudget(server, combination);
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "backpressure"));
    expect(report.accepted).toBeGreaterThan(0);
    // The bound is small and fixed, so an unbounded queue would keep accepting
    // every message and this assertion would fail instead of reporting a count.
    expect(report.accepted).toBeLessThan(64);
  });

  it("ends the stream when the caller cancels it", async () => {
    await expectFreeBudget(server, combination);
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "cancel-stream"));
    expect(report.ok).toBe(true);
    expect(report.canceled).toBe(true);
  });

  it("reports the concurrency limit to the peer instead of waiting on it", async () => {
    // A dedicated endpoint, because the budget is bounded per serving router and
    // shared by every peer: an endpoint reused by the scenarios above can still be
    // releasing their streams, which would make this assertion depend on other tests
    // rather than on the limit itself.
    const dedicated = await PeerNetServer.start(combination.carrier, combination.profile);
    try {
      const { report } = await runFixture(clientArgs(dedicated, combination.carrier, combination.profile, "overloaded"));
      expect(report.ok).toBe(true);
      expect(report.overloaded).toBe(true);
    } finally {
      await dedicated.stop();
    }
  });

  it("returns the serving budget when a peer disappears", async () => {
    const dedicated = await PeerNetServer.start(combination.carrier, combination.profile);
    // A peer that fills the budget and then vanishes, without closing anything: a
    // bounded budget that a departed peer keeps holding is a denial of service that
    // two dead connections are enough to cause.
    const holder = await startHoldingClient([
      "client",
      "--carrier",
      combination.carrier,
      "--addr",
      dedicated.address,
      "--security",
      combination.profile,
      "--dir",
      certificates,
      "--scenario",
      "hold-streams",
      "--streams",
      "2",
    ]);
    try {
      expect(holder.report.ok).toBe(true);
      expect(holder.report.open).toBe(2);
      expect(holder.report.holding).toBe(true);
      // While that peer is connected it owns the budget, so a second peer is refused
      // rather than queued. Without this the recovery assertion below would pass
      // against a budget that was never committed in the first place.
      expect(await budgetIsFull(dedicated, combination), "the holding peer did not fill the serving budget").toBe(true);
    } finally {
      holder.child.kill("SIGKILL");
    }
    expect(
      await waitForFreeBudget(dedicated, combination, 15_000),
      "a departed peer kept consuming the serving budget",
    ).toBe(true);
    await dedicated.stop();
  }, 90_000);

  it("enforces the consumer authorization policy on the serving peer", async () => {
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "authorization"));
    expect(report.ok).toBe(true);
    // An allowed method still runs, so the refusal cannot be a blanket denial.
    expect(report.echo).toBe("allowed");
    expect(report.callDenied).toBe(true);
    expect(report.streamDenied).toBe(true);
  });
});

describe("authenticated profile identity", () => {
  it("reports the identity both peers authenticated as", async () => {
    const server = await PeerNetServer.start("tcp", "mtls");
    try {
      const { report } = await runFixture(clientArgs(server, "tcp", "mtls", "identity"));
      expect(report.ok).toBe(true);
      expect(report.localIdentity).toBe(CLIENT_IDENTITY);
      expect(report.peerIdentity).toBe(SERVER_IDENTITY);
      expect(report.encrypted).toBe(true);
      expect(report.authenticated).toBe(true);
    } finally {
      await server.stop();
    }
  }, 60_000);

  it("reports no identity for the development profile instead of inventing one", async () => {
    const server = await PeerNetServer.start("tcp", "loopback");
    try {
      const { report } = await runFixture(clientArgs(server, "tcp", "loopback", "identity"));
      expect(report.ok).toBe(true);
      // A profile that authenticates nothing must say so rather than report a name
      // it never proved.
      expect(report.localIdentity).toBe("");
      expect(report.peerIdentity).toBe("");
      expect(report.encrypted).toBe(false);
      expect(report.authenticated).toBe(false);
    } finally {
      await server.stop();
    }
  }, 60_000);
});

describe("refusing peers that are not authenticated", () => {
  let server: PeerNetServer;

  beforeAll(async () => {
    server = await PeerNetServer.start("tcp", "mtls");
  }, 60_000);

  afterAll(async () => {
    await server?.stop();
  }, 30_000);

  const refused = [
    { name: "a plaintext peer", profile: "loopback" },
    { name: "a peer whose certificate chains to an unknown anchor", profile: "mtls-untrusted-client" },
    { name: "a peer that trusts no anchor", profile: "mtls-no-roots" },
    { name: "a peer that authenticated as the wrong identity", profile: "mtls-wrong-identity" },
  ];

  it.each(refused)("refuses $name", async ({ profile }) => {
    const { report, code } = await runFixture(clientArgs(server, "tcp", profile, "identity"));
    expect(report.ok).toBe(false);
    expect(code).toBe(1);
    expect(report.error).toBeDefined();
  }, 20_000);

  it("refuses a peer that presents no certificate at all", async () => {
    const { report } = await runFixture(["tls", "--addr", server.address, "--dir", certificates, "--mode", "no-certificate"]);
    expect(report.ok).toBe(true);
    expect(report.admitted).toBe(false);
  });

  it("refuses a peer whose certificate chains to an unknown anchor at the handshake", async () => {
    const { report } = await runFixture(["tls", "--addr", server.address, "--dir", certificates, "--mode", "untrusted-client"]);
    expect(report.ok).toBe(true);
    expect(report.admitted).toBe(false);
  });

  it("refuses unencrypted bytes sent to an encrypted endpoint", async () => {
    const { report } = await runFixture(["tls", "--addr", server.address, "--dir", certificates, "--mode", "plaintext"]);
    expect(report.ok).toBe(true);
    expect(report.admitted).toBe(false);
  });

  it("refuses an encrypted peer that presents no client certificate", async () => {
    const serverWithoutClientCertificates = await PeerNetServer.start("tcp", "mtls");
    try {
      const { report } = await runFixture(
        clientArgs(serverWithoutClientCertificates, "tcp", "mtls-no-certificate", "identity"),
      );
      // The profile cannot even be built without a certificate, so the endpoint is
      // never reachable without presenting one.
      expect(report.ok).toBe(false);
      expect(report.error).toContain("certificate");
    } finally {
      await serverWithoutClientCertificates.stop();
    }
  }, 60_000);
});

describe("refusing insecure profiles on remote endpoints", () => {
  it("refuses the plaintext profile on a non-loopback address", async () => {
    const { report, code } = await runFixture([
      "server",
      "--carrier",
      "tcp",
      "--addr",
      "0.0.0.0:0",
      "--security",
      "loopback",
      "--dir",
      certificates,
    ]);
    expect(report.ok).toBe(false);
    expect(code).toBe(1);
    expect(report.error).toContain("loopback");
  });

  it("refuses the plaintext profile on a hostname that leaves the host", async () => {
    const { report } = await runFixture([
      "client",
      "--carrier",
      "tcp",
      "--addr",
      "example.invalid:9999",
      "--security",
      "loopback",
      "--scenario",
      "identity",
    ]);
    expect(report.ok).toBe(false);
    expect(report.error).toContain("loopback");
  }, 60_000);
});

// Hostile input: the endpoint must reject a frame the codec would never emit and
// stay serving. After every case a normal scenario runs, so "the endpoint survived"
// is asserted, not assumed. These use raw TCP because they test the framing itself,
// below any carrier handshake.
describe("rejecting hostile framing without going down", () => {
  let server: PeerNetServer;

  beforeAll(async () => {
    server = await PeerNetServer.start("tcp", "loopback");
  }, 60_000);

  afterAll(async () => {
    await server?.stop();
  }, 30_000);

  it.each(["malformed-type", "oversized-length", "truncated-body"])(
    "drops the connection on %s instead of accepting it",
    async (wireCase) => {
      const { report, code } = await runFixture(["wire", "--addr", server.address, "--case", wireCase]);
      expect(code).toBe(0);
      expect(report.ok).toBe(true);
      expect(report.closedByPeer).toBe(true);
    },
  );

  it("rejects a randomized corpus of malformed frames without going down", async () => {
    const { report, code } = await runFixture(["wire", "--addr", server.address, "--case", "fuzz-framing"]);
    expect(code).toBe(0);
    expect(report.ok).toBe(true);
    expect(report.iterations).toBeGreaterThan(0);
    expect(report.terminated).toBe(report.iterations);
    // The corpus was dropped connection by connection; the endpoint must still
    // complete an ordinary call afterwards.
    const afterwards = await runFixture(clientArgs(server, "tcp", "loopback", "identity"));
    expect(afterwards.report.ok).toBe(true);
    expect(afterwards.report.echo).toBe("hello");
  });

  it("stays serving after every hostile frame", async () => {
    const { report } = await runFixture(clientArgs(server, "tcp", "loopback", "identity"));
    expect(report.ok).toBe(true);
  });

  it("fails an in-flight call when the session is torn down instead of hanging", async () => {
    const { report, code } = await runFixture(["wire", "--addr", server.address, "--case", "close-race"]);
    expect(code).toBe(0);
    expect(report.ok).toBe(true);
    expect(report.callFailedErr).toBeTruthy();
    expect(report.callFailedErr).not.toContain("deadline");
    expect(report.callFailedErr).not.toContain("secret-value-must-not-leak");
  });
});

// The public facade is the surface a plugin actually imports. These tests run the
// lower-level scenario suite above through it, so "the API consumers use works over
// every supported carrier and profile" is asserted rather than assumed.
describe.each(COMBINATIONS)("public facade over $name", (combination) => {
  it("registers, calls and streams through presentation/peer", async () => {
    const { report, code, stderr } = await runFixture([
      "facade",
      "--carrier",
      combination.carrier,
      "--security",
      combination.profile,
      "--dir",
      certificates,
    ]);
    expect(code, `${report.error ?? ""}\n${stderr}`).toBe(0);
    expect(report.ok).toBe(true);
    expect(report.carrier).toBe(combination.carrier);
    expect(report.securityProfile).toBe(combination.profileName);
    expect(report.encrypted).toBe(combination.encrypted);
    expect(report.authenticated).toBe(combination.authenticated);
    expect(report.echo).toBe("facade");
    expect(report.received).toEqual(["x", "y", "z"]);
  });

  it("enforces the authorization policy through the public facade", async () => {
    const { report } = await runFixture([
      "facade",
      "--carrier",
      combination.carrier,
      "--security",
      combination.profile,
      "--dir",
      certificates,
    ]);
    expect(report.ok, report.error).toBe(true);
    expect(report.callDenied).toBe(true);
    expect(report.streamDenied).toBe(true);
  });

  it("reports the identity both peers authenticated as", () => {
    if (!combination.authenticated) {
      return;
    }
    return runFixture([
      "facade",
      "--carrier",
      combination.carrier,
      "--security",
      combination.profile,
      "--dir",
      certificates,
    ]).then(({ report }) => {
      expect(report.ok, report.error).toBe(true);
      expect(report.localIdentity).toBe(CLIENT_IDENTITY);
      expect(report.peerIdentity).toBe(SERVER_IDENTITY);
    });
  });
});

describe("refusing combinations a carrier cannot offer", () => {
  it("refuses the unauthenticated profile on an always-encrypted carrier", async () => {
    const { report, code } = await runFixture([
      "server",
      "--carrier",
      "quic",
      "--addr",
      "127.0.0.1:0",
      "--security",
      "loopback",
      "--dir",
      certificates,
    ]);
    expect(report.ok).toBe(false);
    expect(code).toBe(1);
    expect(report.error).toContain("loopback");
  });

  it("refuses an unknown carrier instead of silently falling back", async () => {
    const { report } = await runFixture([
      "client",
      "--carrier",
      "carrier-that-does-not-exist",
      "--addr",
      "127.0.0.1:1",
      "--security",
      "mtls",
      "--dir",
      certificates,
      "--scenario",
      "identity",
    ]);
    expect(report.ok).toBe(false);
    expect(report.error).toContain("carrier");
  });
});

describe("refusing to leak secrets in failures", () => {
  it("keeps certificate material and secrets out of every reported error", async () => {
    const server = await PeerNetServer.start("tcp", "mtls");
    try {
      const outcomes = await Promise.all(
        ["loopback", "mtls-untrusted-client", "mtls-no-roots", "mtls-wrong-identity"].map(async (profile) => {
          const { report, stderr } = await runFixture(clientArgs(server, "tcp", profile, "identity"));
          return `${report.error ?? ""}${stderr}`;
        }),
      );
      for (const text of outcomes) {
        expect(text).not.toContain("BEGIN CERTIFICATE");
        expect(text).not.toContain("PRIVATE KEY");
        expect(text).not.toContain("secret-value-must-not-leak");
      }
    } finally {
      await server.stop();
    }
  }, 120_000);
});

function soakArgs(server: PeerNetServer, carrier: string, profile: string, sessions: number, concurrency: number): string[] {
  return [
    "soak",
    "--carrier",
    carrier,
    "--addr",
    server.address,
    "--security",
    profile,
    "--dir",
    certificates,
    "--sessions",
    String(sessions),
    "--concurrency",
    String(concurrency),
  ];
}

function clientArgs(server: PeerNetServer, carrier: string, profile: string, scenario: string): string[] {
  return [
    "client",
    "--carrier",
    carrier,
    "--addr",
    server.address,
    "--security",
    profile,
    "--dir",
    certificates,
    "--scenario",
    scenario,
  ];
}
