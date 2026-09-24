import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("remote dispatch readiness contract", () => {
  it("requires all Ready replicas to agree on settings and release digests", async () => {
    const deployment = JSON.parse(await readFile(`${root}/contracts/protocol/v1/remote-deployment.json`, "utf8"));
    expect(deployment.modes.remote.serviceReadiness.readyReplicasMustAgreeOn).toEqual([
      "protocolVersion",
      "manifestDigest",
      "settingsDigest",
      "releaseDigest",
    ]);
  });

  it("requires the apply acknowledgement from every replica before activation", async () => {
    const deployment = JSON.parse(await readFile(`${root}/contracts/protocol/v1/remote-deployment.json`, "utf8"));
    expect(deployment.modes.remote.dispatchApply.activationBarrier).toBe("gateway-requires-an-acknowledgement-from-every-ready-replica-before-caddy-activation");
  });
});
