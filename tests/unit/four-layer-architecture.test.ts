import { readdir, readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("four-layer plugin protocol SDK", () => {
  it("has explicit domain, application, infrastructure, and presentation packages", async () => {
    const entries = new Set(await readdir(root));
    expect([...entries].filter((entry) => ["domain", "application", "infrastructure", "presentation"].includes(entry)))
      .toEqual(expect.arrayContaining(["domain", "application", "infrastructure", "presentation"]));
  });

  it("keeps the public SDK package as a presentation facade over application code", async () => {
    const facade = await readFile(`${root}/presentation/sdk/sdk.go`, "utf8");
    expect(facade).toMatch(/github\.com\/Liapoldus\/pluginprotocol\/(application|infrastructure)/);
    expect(facade).not.toMatch(/grpc\.BidiStreamingServer|RegisterPluginServiceServer/);
    const entries = new Set(await readdir(root));
    expect(entries.has("sdk")).toBe(false);
    expect(entries.has("transport")).toBe(false);
  });

  it("keeps gRPC security and sockets inside protocol infrastructure", async () => {
    const coreImports = await readFile(
      `${root}/infrastructure/transport/client.go`,
      "utf8",
    );
    expect(coreImports).toMatch(/package transport/);
    expect(coreImports).toMatch(/crypto\/tls/);
    expect(coreImports).toMatch(/google\.golang\.org\/grpc/);
  });

  it("keeps generated protobuf types out of domain and application", async () => {
    const domain = await readFile(`${root}/domain/handlers.go`, "utf8");
    const registry = await readFile(`${root}/application/registry.go`, "utf8");
    const adapter = await readFile(`${root}/infrastructure/grpc/service.go`, "utf8");

    expect(domain).not.toContain("pluginv1");
    expect(registry).not.toContain("pluginv1");
    expect(domain).toContain("type InvocationMode");
    expect(domain).toContain("type CallRequest struct");
    expect(adapter).toContain("func callRequestFromProto");
    expect(adapter).toContain("func streamMessageFromProto");
    expect(adapter).toContain("func streamMessageToProto");
  });

  it("gives one shared contract package ownership of embedded generic contracts", async () => {
    const assets = await readFile(`${root}/contracts/embed.go`, "utf8");
    const facade = await readFile(`${root}/presentation/sdk/sdk.go`, "utf8");
    const infrastructureAssetsExist = (await readdir(`${root}/infrastructure/contracts`)).includes("assets.go");

    expect(assets).toContain("//go:embed protocol http admin-ui");
    expect(infrastructureAssetsExist).toBe(false);
    expect(facade).toMatch(/func ContractFiles\(\) fs\.FS \{ return contractassets\.Files\(\) \}/);
  });

  it("exposes loopback and grant-broker listeners through the public SDK facade", async () => {
    const facade = await readFile(`${root}/presentation/sdk/network.go`, "utf8");
    const implementation = await readFile(`${root}/infrastructure/transport/local_listener.go`, "utf8");

    expect(facade).toMatch(/func ListenLoopback\(/);
    expect(facade).toMatch(/func StartGrantBroker\(/);
    expect(implementation).toMatch(/func ListenLoopback\(/);
    expect(implementation).toMatch(/func StartGrantBroker\(/);
  });
});
