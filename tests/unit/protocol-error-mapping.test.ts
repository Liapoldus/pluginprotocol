import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("versioned protocol error mapping", () => {
  it("defines SDK error classification and delegates operation-specific gRPC statuses", async () => {
    const mapping = JSON.parse(await readFile(`${root}/contracts/protocol/v1/error-mapping.json`, "utf8"));

    expect(mapping).toMatchObject({
      protocolVersion: "liapoldus.plugin.v1",
      client: {
        localCallValidation: "ErrProtocolViolation",
        callResponseCode: "ErrCallRejected",
        contextCancellation: "context.Canceled",
        contextDeadline: "context.DeadlineExceeded",
        otherGrpcFailure: "ErrUnavailable",
        grantRpcFailure: "ErrGrantRejected",
      },
      operationSpecificStatusContracts: [
        "stream-lifecycle.json#/status",
        "dispatch-apply.json#/grpcStatus",
      ],
    });
  });

  it("provides executable client-error vectors for the public SDK", async () => {
    const vectors = JSON.parse(await readFile(`${root}/contracts/protocol/v1/error-mapping-vectors.json`, "utf8"));

    expect(vectors.map(({ name }: { name: string }) => name)).toEqual([
      "local-invalid-call-json",
      "application-call-error-code",
      "other-grpc-status",
      "caller-cancellation",
      "caller-deadline",
      "grant-broker-denial",
    ]);
  });
});
