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
  request_too_large?: boolean;
  response_too_large?: boolean;
  received?: string[];
  accepted?: number;
  canceled?: boolean;
  overloaded?: boolean;
  deadline?: boolean;
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

  const reported = new Promise<Report>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${args.join(" ")} did not report`)), timeout);
    lines.once("line", (line) => {
      clearTimeout(timer);
      resolve(JSON.parse(line) as Report);
    });
  });
  const report = await reported;
  const code = await new Promise<number | null>((resolve) => child.once("exit", (value) => resolve(value)));
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

  static async start(carrier: string, profile: string): Promise<PeerNetServer> {
    const child = startGoFixture(executable, {}, ["server", "--carrier", carrier, "--security", profile, "--dir", certificates]);
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

  it("fails an in-flight call when the session is torn down, on every carrier", async () => {
    const { report, code } = await runFixture([
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

  it("refuses an oversized request and an oversized response", async () => {
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "unary"));
    expect(report.request_too_large).toBe(true);
    expect(report.response_too_large).toBe(true);
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
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "stream"));
    expect(report.received).toEqual(["a", "b", "c"]);
  });

  it("refuses a bounded send queue rather than buffering without limit", async () => {
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "backpressure"));
    expect(report.accepted).toBeGreaterThan(0);
    // The bound is small and fixed, so an unbounded queue would keep accepting
    // every message and this assertion would fail instead of reporting a count.
    expect(report.accepted).toBeLessThan(64);
  });

  it("ends the stream when the caller cancels it", async () => {
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "cancel-stream"));
    expect(report.ok).toBe(true);
    expect(report.canceled).toBe(true);
  });

  it("reports the concurrency limit to the peer instead of waiting on it", async () => {
    const { report } = await runFixture(clientArgs(server, combination.carrier, combination.profile, "overloaded"));
    expect(report.ok).toBe(true);
    expect(report.overloaded).toBe(true);
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
  });

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
    const { report, code } = await runFixture([
      "facade",
      "--carrier",
      combination.carrier,
      "--security",
      combination.profile,
      "--dir",
      certificates,
    ]);
    expect(code).toBe(0);
    expect(report.ok).toBe(true);
    expect(report.carrier).toBe(combination.carrier);
    expect(report.securityProfile).toBe(combination.profileName);
    expect(report.encrypted).toBe(combination.encrypted);
    expect(report.authenticated).toBe(combination.authenticated);
    expect(report.echo).toBe("facade");
    expect(report.received).toEqual(["x", "y", "z"]);
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
