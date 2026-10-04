import { readdir, readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

async function goSources(directory: string): Promise<{ path: string; source: string }[]> {
  const entries = await readdir(directory);
  const files = entries.filter((entry) => entry.endsWith(".go"));
  return Promise.all(
    files.map(async (file) => ({
      path: `${directory}/${file}`,
      source: await readFile(`${directory}/${file}`, "utf8"),
    })),
  );
}

async function goFiles(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const nested = await Promise.all(
    entries.map(async (entry) => {
      if (entry.name === "node_modules" || entry.name === ".git") return [];
      const path = `${directory}/${entry.name}`;
      if (entry.isDirectory()) return goFiles(path);
      return entry.name.endsWith(".go") ? [path] : [];
    }),
  );
  return nested.flat();
}

function importsOf(source: string): string[] {
  return [...source.matchAll(/^import\s*\(([\s\S]*?)^\)/gm)]
    .flatMap(([, block]) => [...block.matchAll(/"([^"]+)"/g)].map(([, specifier]) => specifier))
    .concat([...source.matchAll(/^import\s+"([^"]+)"/gm)].map(([, specifier]) => specifier));
}

const FORBIDDEN_IN_DOMAIN = [
  "Bootstrap",
  "Manifest",
  "ConfigSchema",
  "ConfigApply",
  "Reload",
  "Rollback",
  "Shutdown",
  "DispatchApply",
  "Grant",
  "InstanceID",
  "Replica",
  "Workload",
  "Trust",
  "IdP",
  "Token",
  "Idempotency",
  "Readiness",
  "pluginv1",
  "grpc",
  "protobuf",
  "protoimpl",
];

const FORBIDDEN_IN_APPLICATION = [
  "Bootstrap",
  "Manifest",
  "ConfigApply",
  "Reload",
  "Grant",
  "InstanceID",
  "Replica",
  "Trust",
  "IdP",
  "Token",
  "pluginv1",
  "grpc",
  "net/http",
  "crypto/tls",
];

describe("generic peer domain boundary", () => {
  it("depends on the standard library only", async () => {
    const sources = await goSources(`${root}/domain/peer`);
    expect(sources.length).toBeGreaterThan(0);
    for (const file of sources) {
      for (const specifier of importsOf(file.source)) {
        expect(specifier, `${file.path} must not import ${specifier}`).not.toContain("github.com/Liapoldus");
      }
    }
  });

  it("keeps product and lifecycle concepts out of the generic model", async () => {
    const sources = await goSources(`${root}/domain/peer`);
    for (const file of sources) {
      for (const term of FORBIDDEN_IN_DOMAIN) {
        expect(file.source, `${file.path} must not mention ${term}`).not.toContain(term);
      }
    }
  });

  it("does not depend on a concrete transport", async () => {
    // A stdlib-only domain could still reach for net and pass an import check that
    // only looks at the module's own packages. Transport neutrality is the claim that
    // a deployment can change carrier without changing registrations or call sites, so
    // the concrete carriers have to be absent from the domain, not merely avoided by
    // convention.
    const forbiddenImports = ["net", "net/tcp", "net/http", "crypto/tls", "google.golang.org/grpc"];
    for (const file of await goSources(`${root}/domain/peer`)) {
      for (const specifier of importsOf(file.source)) {
        expect(forbiddenImports, `${file.path} must not import ${specifier}`).not.toContain(specifier);
        expect(specifier, `${file.path} must not import ${specifier}`).not.toContain("quic-go");
      }
      expect(file.source, `${file.path} must not select from a transport package`).not.toMatch(/\b(net|http|tls|quic)\.[A-Z]/);
    }
  });

  it("declares a transport-independent carrier port", async () => {
    const sources = await goSources(`${root}/domain/peer`);
    const joined = sources.map((file) => file.source).join("\n");
    expect(joined).toMatch(/type Session interface/);
    expect(joined).toMatch(/type Carrier interface/);
    expect(joined).toMatch(/type Listener interface/);
    expect(joined).toMatch(/type Authorizer interface/);
    expect(joined).toMatch(/type StatusCode uint8/);
  });
});

describe("generic peer application boundary", () => {
  it("depends on the domain only, never on infrastructure or presentation", async () => {
    // This direction is what carries the remaining ownership claims. The bounded
    // budget, the resolved limits and the dispatch path are all asserted from running
    // peers instead: the overload scenario, the departed-peer scenario and the
    // default-limits scenario observe them over every carrier. None of those can fail
    // here, because if the budget moved down into infrastructure this layer would have
    // to import it, and the assertion below is what would stop it.
    const sources = await goSources(`${root}/application/peer`);
    expect(sources.length).toBeGreaterThan(0);
    for (const file of sources) {
      for (const specifier of importsOf(file.source)) {
        expect(specifier, `${file.path} must not import ${specifier}`).not.toContain("/infrastructure");
        expect(specifier).not.toContain("/presentation");
      }
    }
  });

  it("keeps product and lifecycle concepts out of the generic router", async () => {
    const sources = await goSources(`${root}/application/peer`);
    for (const file of sources) {
      for (const term of FORBIDDEN_IN_APPLICATION) {
        expect(file.source, `${file.path} must not mention ${term}`).not.toContain(term);
      }
    }
  });
});

describe("removed legacy surface", () => {
  it("keeps only peer fixtures in the test fixture directory", async () => {
    expect((await readdir(`${root}/tests/fixtures`)).sort()).toEqual(["peer-net", "peer-router", "stream-half-close", "stream-terminal-drain"]);
  });

  it("does not import the Plugin SDK or any legacy root anywhere", async () => {
    const forbidden = [
      "github.com/Liapoldus/plugin-sdk",
      '"github.com/Liapoldus/pluginprotocol/pluginv1"',
      '"github.com/Liapoldus/pluginprotocol/contracts"',
      '"github.com/Liapoldus/pluginprotocol/presentation/sdk"',
      '"github.com/Liapoldus/pluginprotocol/domain"',
      '"github.com/Liapoldus/pluginprotocol/application"',
      '"github.com/Liapoldus/pluginprotocol/infrastructure/transport"',
      '"github.com/Liapoldus/pluginprotocol/infrastructure/grpc"',
      '"github.com/Liapoldus/pluginprotocol/infrastructure/contracts"',
    ];
    const offenders: string[] = [];
    for (const file of await goFiles(root)) {
      const source = await readFile(file, "utf8");
      for (const specifier of forbidden) {
        if (source.includes(specifier)) offenders.push(`${file} references ${specifier}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("keeps only the generic peer contract under proto/liapoldus", async () => {
    const proto = await readFile(`${root}/proto/liapoldus/peer/v1/peer.proto`, "utf8");
    expect(proto).toContain("package liapoldus.peer.v1");
  });
});

// The public authorization surface needs no source assertion: the facade scenario
// installs a consumer authorizer and proves an allow and a deny over every carrier,
// and tests/fixtures/peer-net/facade.go would not compile if the entry point stopped
// existing.

// Every name the public facade exports. A consumer guide that stops covering one
// of them is a guide that sends a reader to the source, so the set is asserted
// against the documentation rather than left to review.
const PUBLIC_FACADE_SYMBOLS = [
  "Addr",
  "AllowAll",
  "Authenticated",
  "Authorizer",
  "Build",
  "Call",
  "Carrier",
  "CarrierQUIC",
  "CarrierTCP",
  "Certificate",
  "Client",
  "ClientConfig",
  "Close",
  "DefaultLimits",
  "Dial",
  "Encrypted",
  "Endpoint",
  "ErrCanceled",
  "ErrClosed",
  "ErrDeadlineExceeded",
  "ErrInternal",
  "ErrInvalidRequest",
  "ErrMessageTooLarge",
  "ErrMethodNotFound",
  "ErrOverloaded",
  "ErrProtocolViolation",
  "ErrSendQueueFull",
  "ErrStreamClosed",
  "ErrUnauthorized",
  "ErrUnavailable",
  "Handler",
  "HandshakeTimeout",
  "Identity",
  "KeepAlive",
  "Limits",
  "Listen",
  "Method",
  "Message",
  "Network",
  "NetworkConfig",
  "NewRegistry",
  "PeerIdentity",
  "PlaintextLoopback",
  "Profile",
  "RegisterCall",
  "RegisterStream",
  "RegistryBuilder",
  "Result",
  "Roots",
  "Security",
  "SecurityConfig",
  "Server",
  "ServerConfig",
  "ServerName",
  "Sessions",
  "Stream",
  "WithAuthorizer",
  "WithLimits",
];

// exportedSymbols reads the package's own declarations: top-level func, type, var and
// const names, the names inside grouped var and const blocks, and exported methods.
// It reads declarations rather than documentation on purpose, so a new export has to
// be described for this check to stay green.
async function exportedSymbols(directory: string): Promise<string[]> {
  const symbols = new Set<string>();
  for (const file of await goSources(directory)) {
    const { source, path } = file;
    for (const match of source.matchAll(/^(?:func|type|var|const)\s+([A-Z]\w*)/gm)) {
      symbols.add(match[1]);
    }
    for (const match of source.matchAll(/^(?:func|type|var|const)\s+\(\n([\s\S]*?)^\)/gm)) {
      for (const name of match[1].matchAll(/^\s+([A-Z]\w*)/gm)) {
        symbols.add(name[1]);
      }
    }
    for (const match of source.matchAll(/^func\s+\([^)]*\)\s+([A-Z]\w*)/gm)) {
      symbols.add(match[1]);
    }
    expect(path).toContain(directory);
  }
  return [...symbols].sort();
}

describe("consumer documentation", () => {
  it("documents every public facade entry point", async () => {
    const guide = await readFile(`${root}/docs/consumer-guide.md`, "utf8");
    expect(PUBLIC_FACADE_SYMBOLS.filter((symbol) => !guide.includes(symbol))).toEqual([]);
  });

  it("documents every symbol the facade actually exports", async () => {
    // The list above only fails when a documented name disappears. This runs the other
    // way, because an export added without a line in the guide is the drift that
    // matters: the compiler will not notice it, the runtime scenarios call what they
    // already knew about, and a consumer would find it by reading the package.
    const guide = await readFile(`${root}/docs/consumer-guide.md`, "utf8");
    const exported = await exportedSymbols(`${root}/presentation/peer`);
    // Two names the extractor has to find for the comparison below to mean anything.
    expect(exported).toContain("RegistryBuilder");
    expect(exported).toContain("Dial");
    expect(exported).toContain("Message");
    expect(exported.filter((symbol) => !guide.includes(symbol))).toEqual([]);
  });

  it("keeps the guide on the facade and off the layers below it", async () => {
    const guide = await readFile(`${root}/docs/consumer-guide.md`, "utf8");
    expect(guide).toContain('import publicpeer "github.com/Liapoldus/pluginprotocol/v2/presentation/peer"');
    for (const layer of ["pluginprotocol/domain", "pluginprotocol/application", "pluginprotocol/infrastructure", "pluginprotocol/pluginv1"]) {
      expect(guide, `the guide must not send a consumer into ${layer}`).not.toContain(layer);
    }
  });

  it("points the guide at an example the suite actually compiles and runs", async () => {
    const guide = await readFile(`${root}/docs/consumer-guide.md`, "utf8");
    expect(guide).toContain("tests/fixtures/peer-net/facade.go");
    const fixture = await readFile(`${root}/tests/fixtures/peer-net/facade.go`, "utf8");
    expect(fixture).toContain("pluginprotocol/v2/presentation/peer");
  });

  it("states what the library does not own", async () => {
    const guide = await readFile(`${root}/docs/consumer-guide.md`, "utf8");
    expect(guide).toContain("What this library does not do");
    for (const excluded of ["Core", "Reload", "lifecycle", "plugin-sdk"]) {
      expect(guide, `the guide must record ${excluded} as out of scope`).toContain(excluded);
    }
  });
});
