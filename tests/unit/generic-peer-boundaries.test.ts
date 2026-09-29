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

  it("does not depend on the legacy root domain package", async () => {
    const sources = await goSources(`${root}/domain/peer`);
    for (const file of sources) {
      expect(file.source, `${file.path} must not import the legacy domain root`).not.toContain(
        '"github.com/Liapoldus/pluginprotocol/domain"',
      );
    }
  });

  it("exposes an opaque, consumer-defined method identity", async () => {
    const sources = await goSources(`${root}/domain/peer`);
    const joined = sources.map((file) => file.source).join("\n");
    expect(joined).toMatch(/type Method string/);
    expect(joined).toMatch(/type PeerIdentity struct/);
    expect(joined).toMatch(/func \(limits Limits\) WithDefaults\(\) Limits/);
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

  it("does not depend on the legacy root application package", async () => {
    const sources = await goSources(`${root}/application/peer`);
    for (const file of sources) {
      expect(file.source, `${file.path} must not import the legacy application root`).not.toContain(
        '"github.com/Liapoldus/pluginprotocol/application"',
      );
    }
  });

  it("dispatches through the domain router with a bounded budget", async () => {
    const sources = await goSources(`${root}/application/peer`);
    const joined = sources.map((file) => file.source).join("\n");
    expect(joined).toMatch(/func NewRouter\(/);
    expect(joined).toMatch(/func \(router \*Router\) Invoke\(/);
    expect(joined).toMatch(/func \(router \*Router\) ServeStream\(/);
    expect(joined).toContain("ErrOverloaded");
  });
});

describe("generic peer conformance assets", () => {
  it("drives the real Go implementation from a child-process fixture", async () => {
    const fixture = await readFile(`${root}/tests/fixtures/peer-router/main.go`, "utf8");
    expect(fixture).toMatch(/^package main$/m);
    expect(fixture).toContain("github.com/Liapoldus/pluginprotocol/application/peer");
    expect(fixture).toContain("github.com/Liapoldus/pluginprotocol/domain/peer");
  });
});
