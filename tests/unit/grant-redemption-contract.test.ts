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
    expect(launch).toMatchObject({
      bootstrap: { rpc: "PluginService.Bootstrap", containsSecretMaterial: false },
      secrets: { delivery: "scoped GrantBroker RedeemGrant only", inBootstrap: false, inConfigApply: "opaque secret-reference identifiers only" },
    });
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

  it("separates per-call and config-revision grants without carrying secret bytes", async () => {
    const grantProto = await readFile(`${root}/proto/liapoldus/plugin/v1/grant.proto`, "utf8");
    const controlProto = await readFile(`${root}/proto/liapoldus/plugin/v1/control.proto`, "utf8");
    const source = await readFile(`${root}/transport/grants.go`, "utf8");

    expect(grantProto).toContain("GRANT_SCOPE_CALL");
    expect(grantProto).toContain("GRANT_SCOPE_CONFIG_APPLY");
    expect(grantProto).toContain("settings_revision");
    expect(grantProto).toContain("secret_reference");
    expect(controlProto).toMatch(/message ConfigApplyRequest\s*\{[^}]*settings_revision[^}]*repeated ActiveGrant grants/s);
    expect(controlProto).toMatch(/message ConfigApplyResult\s*\{[^}]*settings_revision/s);
    expect(source).toMatch(/func \(c \*GrantClient\) RedeemConfig\(ctx context\.Context, grant \*pluginv1\.ActiveGrant\)/);
    expect(source).toMatch(/GRANT_SCOPE_CONFIG_APPLY/);
    expect(source).not.toMatch(/message (?:ActiveGrant|RedeemGrantRequest)[\s\S]*?bytes secret/);
  });

  it("keeps grpc implementation types behind the protocol transport API", async () => {
    const transport = await readFile(`${root}/transport/grants.go`, "utf8");

    expect(transport).toContain("type GrantServer struct");
    expect(transport).toMatch(/func NewGrantBrokerServer\(service pluginv1\.GrantBrokerServer\) \*GrantServer/);
    expect(transport).toContain("func (s *GrantServer) Serve(listener net.Listener) error");
    expect(transport).toContain("func (s *GrantServer) Stop()");
  });
});
