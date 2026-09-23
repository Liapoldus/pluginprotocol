import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("scoped grant redemption v1", () => {
  it("uses a typed Gateway callback and keeps secret bytes out of capability JSON", async () => {
    const grantProto = await readFile(`${root}/proto/liapoldus/plugin/v1/grant.proto`, "utf8");
    const serviceProto = await readFile(`${root}/proto/liapoldus/plugin/v1/service.proto`, "utf8");
    const launch = JSON.parse(await readFile(`${root}/contracts/protocol/v1/launch.json`, "utf8")) as Record<string, unknown>;

    expect(grantProto).toContain("service GrantBroker");
    expect(grantProto).toMatch(/rpc RedeemGrant\(RedeemGrantRequest\) returns \(RedeemGrantResponse\)/);
    expect(grantProto).toContain("message RedeemGrantRequest");
    expect(grantProto).toContain("bytes secret");
    expect(serviceProto).toContain("repeated ActiveGrant grants = 3");
    expect(serviceProto).not.toMatch(/message CallRequest[\s\S]*?bytes secret/);
    expect(launch).toHaveProperty("grantBrokerEndpointEnvironment", "LIAPOLDUS_GRANT_BROKER_ENDPOINT");
  });

  it("generates test-only stubs for the typed grant broker service", async () => {
    const source = await readFile(`${root}/tests/generated/liapoldus/plugin/v1/grant.ts`, "utf8");

    expect(source).toContain("GrantBrokerClient");
    expect(source).toContain("GrantBrokerService");
    expect(source).toContain("/liapoldus.plugin.v1.GrantBroker/RedeemGrant");
    expect(source).toContain("RedeemGrantResponse");
  });

  it("binds each handle and redemption request to one capability", async () => {
    const grantProto = await readFile(`${root}/proto/liapoldus/plugin/v1/grant.proto`, "utf8");

    expect(grantProto).toMatch(/message ActiveGrant[\s\S]*?string capability = 4/);
    expect(grantProto).toMatch(/message RedeemGrantRequest[\s\S]*?string capability = 4/);
    expect(grantProto).toContain("string purpose = 2");
  });

  it("keeps grpc implementation types behind the protocol transport API", async () => {
    const transport = await readFile(`${root}/transport/grants.go`, "utf8");

    expect(transport).toContain("type GrantServer struct");
    expect(transport).toMatch(/func NewGrantBrokerServer\(service pluginv1\.GrantBrokerServer\) \*GrantServer/);
    expect(transport).toContain("func (s *GrantServer) Serve(listener net.Listener) error");
    expect(transport).toContain("func (s *GrantServer) Stop()");
  });
});
