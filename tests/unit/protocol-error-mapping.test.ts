import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("versioned protocol error mapping", () => {
  it("defines SDK error classification and delegates operation-specific gRPC statuses", async () => {
    const mapping = JSON.parse(await readFile(`${root}/contracts/protocol/v1/error-mapping.json`, "utf8"));

    expect(mapping).toMatchObject({
      protocolVersion: "liapoldus.plugin.v1",
      sdk: {
        PluginServiceClient: {
          localCallValidation: "ErrProtocolViolation",
          invalidCallResponseJson: "ErrProtocolViolation",
          callResponseCode: "ErrCallRejected",
          unaryGrpcStatus: {
            CANCELLED: "context.Canceled",
            DEADLINE_EXCEEDED: "context.DeadlineExceeded",
            other: "ErrUnavailable",
          },
          streamFailure: "raw gRPC status is returned to the caller",
        },
        GrantClient: {
          rpcFailure: "ErrGrantRejected",
        },
      },
      operationSpecificStatusContracts: [
        "stream-lifecycle.json#/status",
        "dispatch-apply.json#/grpcStatus",
      ],
    });

    const stream = JSON.parse(await readFile(`${root}/contracts/protocol/v1/stream-lifecycle.json`, "utf8"));
    const dispatch = JSON.parse(await readFile(`${root}/contracts/protocol/v1/dispatch-apply.json`, "utf8"));
    expect(stream.status).toMatchObject({ invalidMessage: "INVALID_ARGUMENT", limitExceeded: "RESOURCE_EXHAUSTED" });
    expect(dispatch.grpcStatus).toMatchObject({
      malformedRequestOrScope: "INVALID_ARGUMENT",
      authenticatedInstanceOrScopeMismatch: "PERMISSION_DENIED",
      staleOrConflictingGeneration: "FAILED_PRECONDITION",
      localActiveSettingsOrReleaseMismatch: "FAILED_PRECONDITION",
    });
  });

  it("provides executable client-error vectors for the public SDK", async () => {
    const vectors = JSON.parse(await readFile(`${root}/contracts/protocol/v1/error-mapping-vectors.json`, "utf8"));

    expect(vectors.map(({ name }: { name: string }) => name)).toEqual([
      "local-invalid-call-json",
      "invalid-call-response-json",
      "application-call-error-code",
      "other-grpc-status",
      "caller-cancellation",
      "caller-deadline",
      "grant-broker-denial",
      "grant-broker-empty-secret",
      "grant-broker-nil-response",
      "grant-broker-oversized-secret",
    ]);
  });
});
