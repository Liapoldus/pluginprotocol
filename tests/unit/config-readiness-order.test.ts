import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("Gateway-pushed plugin configuration readiness", () => {
  it("applies Gateway settings before reporting plugin health/readiness", async () => {
    const source = await readFile(`${root}/transport/client.go`, "utf8");
    const handshake = source.slice(source.indexOf("func (c *Client) handshake("));
    const schema = handshake.indexOf("c.service.ConfigSchema(");
    const apply = handshake.indexOf("c.service.ConfigApply(");
    const health = handshake.indexOf("c.CheckHealth(ctx)");

    expect(schema).toBeGreaterThanOrEqual(0);
    expect(apply).toBeGreaterThan(schema);
    expect(health).toBeGreaterThan(apply);
    expect(handshake).toMatch(/if !result\.GetApplied\(\)[\s\S]*?return Handshake\{\}, ErrUnavailable/);
  });

  it("exposes typed operational bootstrap before the pushed settings handshake", async () => {
    const source = await readFile(`${root}/transport/client.go`, "utf8");
    const launch = JSON.parse(await readFile(`${root}/contracts/protocol/v1/launch.json`, "utf8"));

    expect(source).toMatch(/func \(c \*Client\) BootstrapAndHandshake\(ctx context\.Context, bootstrap \*pluginv1\.BootstrapRequest, config \[\]byte, settingsRevision string, grants \[\]\*pluginv1\.ActiveGrant\)/);
    expect(source).toMatch(/c\.service\.Bootstrap\(ctx, bootstrap\)/);
    expect(launch.configuration.delivery).toBe("PluginService.ConfigApply push RPC before health/readiness");
    expect(launch.bootstrap.containsApplicationConfig).toBe(false);
    expect(launch.bootstrap.containsSecretMaterial).toBe(false);
  });

  it("binds config-secret redemption to opaque references and revision-scoped grants", async () => {
    const contract = JSON.parse(await readFile(`${root}/contracts/protocol/v1/config-apply.json`, "utf8"));
    const source = await readFile(`${root}/transport/grants.go`, "utf8");

    expect(contract.secretReferences).toMatchObject({
      gatewayInput: "may refer to an external file: secret source",
      pluginValue: "opaque Gateway-generated identifier; no path or URI syntax is exposed or standardized",
    });
    expect(contract.grantScope).toMatchObject({
      scope: "GRANT_SCOPE_CONFIG_APPLY",
      bindings: ["plugin instance", "settings revision", "secret reference", "purpose"],
      crossUse: "cannot be used by Call or Stream capability invocations",
    });
    expect(source).toMatch(/func \(c \*GrantClient\) RedeemConfig\(ctx context\.Context, grant \*pluginv1\.ActiveGrant\)/);
    expect(source).toMatch(/Scope: grant\.GetScope\(\)/);
  });
});
