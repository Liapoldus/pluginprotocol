import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("remote mTLS revocation contract", () => {
  it("requires CA-signed, fresh CRLs for both remote TLS directions and GrantBroker", async () => {
    const contract = JSON.parse(
      await readFile(join(root, "contracts/protocol/v1/remote-revocation.json"), "utf8"),
    ) as {
      protocolVersion: string;
      bundle: { encoding: string; pemBlockType: string; issuerSignature: string };
      requiredFor: string[];
      verification: { thisUpdate: string; nextUpdate: string; crlNumber: string; missingIssuerCRL: string };
      update: { valid: string; invalid: string; closesActiveChannels: boolean; reconnectOwner: string; replayUnknownCall: boolean };
      maxBundleBytes: number;
    };

    expect(contract.protocolVersion).toBe("liapoldus.plugin.v1");
    expect(contract.bundle).toMatchObject({ encoding: "PEM", pemBlockType: "X509 CRL" });
    expect(contract.bundle.issuerSignature).toContain("issuing CA");
    expect(contract.requiredFor).toEqual(expect.arrayContaining([
      "remote plugin client",
      "remote plugin server",
      "remote GrantBroker client",
      "remote GrantBroker server",
    ]));
    expect(contract.verification).toMatchObject({
      thisUpdate: "not-in-future",
      nextUpdate: "required-and-future",
      crlNumber: "required-and-monotonic-per-issuer",
      missingIssuerCRL: "deny-handshake",
    });
    expect(contract.update).toMatchObject({
      valid: "atomic-replace",
      invalid: "clear-state-and-fail-closed",
      closesActiveChannels: true,
      reconnectOwner: "caller",
      replayUnknownCall: false,
    });
    expect(contract.maxBundleBytes).toBeGreaterThan(0);

    const deployment = JSON.parse(
      await readFile(join(root, "contracts/protocol/v1/remote-deployment.json"), "utf8"),
    ) as { remoteRevocation: string; grantBroker: { revocationContract: string } };
    const listener = JSON.parse(
      await readFile(join(root, "contracts/protocol/v1/remote-listener.json"), "utf8"),
    ) as { revocationContract: string };
    expect(deployment.remoteRevocation).toBe("remote-revocation.json");
    expect(deployment.grantBroker.revocationContract).toBe("remote-revocation.json");
    expect(listener.revocationContract).toBe("remote-revocation.json");
  });
});
