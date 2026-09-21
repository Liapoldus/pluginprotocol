import { describe, expect, it } from "vitest";
import { runProbe } from "../support/probe.js";

describe("v1 framed transport", () => {
  it("decodes a complete big-endian length-prefixed CALL frame", async () => {
    const result = await runProbe("decode-frame", "000000080801100722026f6b");

    expect(result.exitCode).toBe(0);
    expect(JSON.parse(result.stdout)).toEqual({
      kind: "CALL",
      requestId: "7",
      streamId: "0",
      payloadBase64: "b2s=",
    });
  });

  it("rejects a frame whose declared length exceeds the received bytes", async () => {
    const result = await runProbe("decode-frame", "000000100801100722026f6b");

    expect(result.exitCode).not.toBe(0);
    expect(result.stderr).toContain("unexpected EOF");
  });
});
