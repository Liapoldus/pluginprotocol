import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));

describe("remote v1 mTLS identity separation", () => {
  it("uses distinct replica, Gateway-control, and Caddy-data identities with bounded RPC scopes", async () => {
    const deployment = JSON.parse(await readFile(join(root, "contracts/protocol/v1/remote-deployment.json"), "utf8"));
    const remote = deployment.modes.remote;
    const tls = remote.endpoint.properties.tls;

    expect(tls.required).toEqual(expect.arrayContaining([
      "serverName",
      "expectedServerIdentity",
      "ca",
      "gatewayControlIdentity",
      "caddyDataIdentity",
    ]));
    expect(tls.properties.expectedServerIdentity).toMatchObject({
      type: "unique-uri-san-per-replica",
      format: "urn:liapoldus:plugin:<instance-id>:replica:<replica-id>",
      logicalInstanceMustMatch: true,
    });

    const control = tls.properties.gatewayControlIdentity;
    const data = tls.properties.caddyDataIdentity;
    expect(control.properties.identity.format).toBe("urn:liapoldus:gateway:<deployment-id>:plugin:<instance-id>:control");
    expect(control.properties.allowedRPCs).toEqual(["Manifest", "ConfigSchema", "ConfigApply", "Shutdown", "DispatchApply", "grpc.health.v1"]);
    expect(data.properties.identity.format).toBe("urn:liapoldus:gateway:<deployment-id>:plugin:<instance-id>:data");
    expect(data.properties.allowedRPCs).toEqual(["Call", "Stream"]);
    expect(data.properties.capabilityScope).toMatchObject({
      source: "active-dispatch-generation",
      enforcePerCall: true,
    });
    expect(control.properties.clientCertificate.$ref).toBe("file-reference.schema.json");
    expect(data.properties.clientCertificate.$ref).toBe("file-reference.schema.json");
    expect(control.properties.clientKey).toMatchObject({ $ref: "file-reference.schema.json", minimumPermissions: "owner-read-only" });
    expect(data.properties.clientKey).toMatchObject({ $ref: "file-reference.schema.json", minimumPermissions: "owner-read-only" });

    expect(remote.pluginAuthorization.gatewayControl.allowedRPCs).toEqual(control.properties.allowedRPCs);
    expect(remote.pluginAuthorization.caddyData.allowedRPCs).toEqual(data.properties.allowedRPCs);
  });
});
