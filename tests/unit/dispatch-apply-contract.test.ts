import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("remote dispatch generation apply contract", () => {
  it("defines typed per-replica generation, settings, release and capability scope", async () => {
    const service = await readFile(`${root}/proto/liapoldus/plugin/v1/service.proto`, "utf8");

    expect(service).toMatch(/rpc DispatchApply\(DispatchApplyRequest\) returns \(DispatchApplyResponse\)/);
    expect(service).toMatch(/message DispatchApplyRequest\s*\{[^}]*uint64 generation\s*=\s*1;[^}]*string instance_id\s*=\s*2;[^}]*string settings_digest\s*=\s*3;[^}]*string release_digest\s*=\s*4;[^}]*repeated CapabilityDispatchScope capabilities\s*=\s*5;/s);
    expect(service).toMatch(/message CapabilityDispatchScope\s*\{[^}]*string capability\s*=\s*1;[^}]*repeated InvocationMode modes\s*=\s*2;/s);
    expect(service).toMatch(/message DispatchApplyResponse\s*\{[^}]*uint64 generation\s*=\s*1;[^}]*string replica_identity_uri\s*=\s*2;[^}]*string manifest_digest\s*=\s*3;[^}]*string settings_digest\s*=\s*4;[^}]*string release_digest\s*=\s*5;[^}]*string dispatch_digest\s*=\s*6;/s);
  });

  it("authorizes DispatchApply only as a Gateway-control RPC", async () => {
    const deployment = JSON.parse(await readFile(`${root}/contracts/protocol/v1/remote-deployment.json`, "utf8"));

    expect(deployment.modes.remote.pluginAuthorization.gatewayControl.allowedRPCs).toContain("DispatchApply");
    expect(deployment.modes.remote.pluginAuthorization.caddyData.allowedRPCs).not.toContain("DispatchApply");
  });
});
