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
    const facade = await readFile(`${root}/sdk/service.go`, "utf8");
    expect(facade).toMatch(/github\.com\/Liapoldus\/pluginprotocol\/presentation\/sdk/);
    expect(facade).not.toMatch(/grpc\.BidiStreamingServer|RegisterPluginServiceServer/);
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

  it("exposes loopback and grant-broker listeners through the public SDK facade", async () => {
    const facade = await readFile(`${root}/presentation/sdk/transport.go`, "utf8");
    const implementation = await readFile(`${root}/infrastructure/transport/local_listener.go`, "utf8");

    expect(facade).toMatch(/func ListenLoopback\(/);
    expect(facade).toMatch(/func StartGrantBroker\(/);
    expect(implementation).toMatch(/func ListenLoopback\(/);
    expect(implementation).toMatch(/func StartGrantBroker\(/);
  });
});
