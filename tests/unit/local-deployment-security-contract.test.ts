import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("local deployment security contract", () => {
  it("references the normative launch-scoped mTLS contract instead of plaintext loopback", async () => {
    const deployment = JSON.parse(
      await readFile(`${root}/contracts/protocol/v1/remote-deployment.json`, "utf8"),
    );
    const localMode = deployment.modes.local;
    const securityReference = localMode.channelSecurity.contract;
    expect(securityReference).toBe("local-workload-mtls.json");
    const localSecurity = JSON.parse(
      await readFile(`${root}/contracts/protocol/v1/${securityReference}`, "utf8"),
    );

    expect(localSecurity.security).toMatchObject({
      minimumTLSVersion: 772,
      mutualAuthentication: true,
      certificateTrust: "exact-sha256-leaf-pin",
      insecureFallback: false,
    });
  });
});
