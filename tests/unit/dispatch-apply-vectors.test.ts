import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  CapabilityDispatchScope,
  DispatchApplyRequest,
  DispatchApplyResponse,
  InvocationMode,
} from "../generated/liapoldus/plugin/v1/control.js";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("DispatchApply v1 canonical contract", () => {
  it("defines atomic per-replica authorization replacement and acknowledgement semantics", async () => {
    const contract = JSON.parse(await readFile(`${root}/contracts/protocol/v1/dispatch-apply.json`, "utf8"));

    expect(contract.protocolVersion).toBe("liapoldus.plugin.v1");
    expect(contract.rpc).toBe("PluginService.DispatchApply");
    expect(contract.request.capabilities.meaning).toContain("not a patch");
    expect(contract.request.capabilities.emptyModes).toBe("reject");
    expect(contract.acknowledgement.required).toEqual([
      "generation",
      "replicaIdentityUri",
      "manifestDigest",
      "settingsDigest",
      "releaseDigest",
      "dispatchDigest",
    ]);
    expect(contract.request.required).toEqual([
      "generation",
      "instanceId",
      "settingsDigest",
      "releaseDigest",
      "capabilities",
    ]);
    expect(contract.request.generation).toMatchObject({
      minimum: 1,
      stale: "reject",
      sameGenerationSameRequest: "idempotent",
      sameGenerationDifferentRequest: "reject",
    });
    expect(contract.request.generation.apply).toBe("atomic-replace");
    expect(contract.request.capabilities.emptyCapabilities).toBe("deny-all");
    expect(contract.acknowledgement.replicaIdentityUri).toBe("must-equal-the-verified-remote-plugin-replica-URI-SAN-for-this-connection");
    expect(contract.acknowledgement.after).toBe("authorization-generation-is-active-for-this-replica");
    expect(contract.activationBarrier).toBe("acknowledge-every-ready-replica-before-caddy-activation");
  });

  it("round-trips the canonical typed request and per-replica acknowledgement through generated v1 code", async () => {
    const vector = JSON.parse(await readFile(`${root}/contracts/protocol/v1/dispatch-apply-vectors.json`, "utf8"));
    const request = DispatchApplyRequest.fromJSON(vector.request);
    const requestRoundTrip = DispatchApplyRequest.decode(DispatchApplyRequest.encode(request).finish());
    const response = DispatchApplyResponse.fromJSON(vector.response);
    const responseRoundTrip = DispatchApplyResponse.decode(DispatchApplyResponse.encode(response).finish());

    expect(requestRoundTrip).toEqual(request);
    expect(responseRoundTrip).toEqual(response);
    expect(requestRoundTrip.generation).toBe(responseRoundTrip.generation);
    expect(requestRoundTrip.settingsDigest).toBe(responseRoundTrip.settingsDigest);
    expect(requestRoundTrip.releaseDigest).toBe(responseRoundTrip.releaseDigest);
    expect(responseRoundTrip.replicaIdentityUri).toContain(`:${requestRoundTrip.instanceId}:replica:`);
    expect(requestRoundTrip.capabilities).toEqual([
      CapabilityDispatchScope.fromJSON({
        capability: "example.records.read",
        modes: [InvocationMode.INVOCATION_MODE_CALL, InvocationMode.INVOCATION_MODE_HTTP_STREAM],
      }),
    ]);
  });
});
