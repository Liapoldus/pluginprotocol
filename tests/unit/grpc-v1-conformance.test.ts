import { describe, expect, it } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("gRPC plugin protocol v1 conformance", () => {
  it("pins proto namespace, typed RPC descriptors, and JSON contract vectors", () => {
    const service = readFileSync(`${root}/proto/liapoldus/plugin/v1/service.proto`, "utf8");
    expect(service).toContain("package liapoldus.plugin.v1;");
    expect(service).toContain("rpc Call(CallRequest) returns (CallResponse)");
    expect(service).toContain("rpc Stream(stream StreamMessage) returns (stream StreamMessage)");

    const vectorsPath = `${root}/contracts/protocol/v1/json-payload-vectors.json`;
    expect(existsSync(vectorsPath)).toBe(true);
    const vectors = JSON.parse(readFileSync(vectorsPath, "utf8"));
    expect(vectors.length).toBeGreaterThan(0);
    expect(vectors.every((vector: { capability?: string; request?: unknown; response?: unknown }) =>
      vector.capability && vector.request !== undefined && vector.response !== undefined)).toBe(true);
    expect(JSON.stringify(vectors)).not.toContain("wireHex");
  });

  it("retires all custom frame/session implementations and wire-hex vectors", () => {
    const retiredPaths = [
      "framing",
      "session",
      "cmd/protocol-probe",
      "control/control.go",
      "proto/liapoldus/plugin/v1/frame.proto",
      "proto/liapoldus/plugin/v1/envelope.proto",
      "pluginv1/frame.pb.go",
      "pluginv1/envelope.pb.go",
      "compatibility/v1/golden.json",
    ];
    expect(retiredPaths.filter((path) => existsSync(`${root}/${path}`))).toEqual([]);
  });
});
