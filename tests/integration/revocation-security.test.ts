import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("signed CRL revocation", () => {
  it("fails closed on invalid or stale bundles and closes live sessions on update", () => {
    const result = spawnSync(
      "go",
      ["run", "./tests/fixtures/revocation-security", "--scenario=update"],
      { cwd: root, encoding: "utf8", timeout: 60_000 },
    );

    expect(result.status, result.stderr).toBe(0);
    const report = JSON.parse(result.stdout) as {
      initialHandshake: boolean;
      closedOnUpdate: boolean;
      revokedHandshakeRejected: boolean;
      revokedCallNotDispatched: boolean;
      exactRepeatAccepted: boolean;
      badSignatureRejected: boolean;
      rollbackNumberRejected: boolean;
      nonMonotonicRemovalRejected: boolean;
      trustRootChangeRejected: boolean;
      expiredBundleRejected: boolean;
      unknownIssuerRejected: boolean;
      expiryClosesSession: boolean;
      secretsRedacted: boolean;
      checkpointComplete: boolean;
      checkpointRestored: boolean;
      intermediateIssuerAccepted: boolean;
    };

    expect(report).toEqual({
      initialHandshake: true,
      closedOnUpdate: true,
      revokedHandshakeRejected: true,
      revokedCallNotDispatched: true,
      exactRepeatAccepted: true,
      badSignatureRejected: true,
      rollbackNumberRejected: true,
      nonMonotonicRemovalRejected: true,
      trustRootChangeRejected: true,
      expiredBundleRejected: true,
      unknownIssuerRejected: true,
      secretsRedacted: true,
      checkpointComplete: true,
      checkpointRestored: true,
      intermediateIssuerAccepted: true,
      expiryClosesSession: true,
    });
  });

  it("rejects stripped and root-incomplete checkpoints and negative serials on restore or apply", () => {
    const result = spawnSync(
      "go",
      ["run", "./tests/fixtures/revocation-security", "--scenario=restore-guards"],
      { cwd: root, encoding: "utf8", timeout: 60_000 },
    );

    expect(result.status, result.stderr).toBe(0);
    expect(JSON.parse(result.stdout)).toEqual({
      strippedCheckpointRejected: true,
      missingRootIssuerRejected: true,
      negativeSerialApplyRejected: true,
      managerUsableAfterRejection: true,
    });
  });
});
