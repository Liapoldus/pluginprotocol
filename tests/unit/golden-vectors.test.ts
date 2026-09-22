import { describe, expect, it } from "vitest";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { runProbe } from "../support/probe.js";

type Vector = {
  name: string;
  wireHex: string;
  kind: string;
  requestId: string;
  streamId: string;
  payloadBase64: string;
};

const vectorsPath = fileURLToPath(new URL("../../compatibility/v1/golden.json", import.meta.url));

describe("liapoldus.plugin.v1 golden vectors", () => {
  it("keeps every published wire vector decodable", async () => {
    const vectors = JSON.parse(await readFile(vectorsPath, "utf8")) as Vector[];
    expect(vectors.length).toBeGreaterThan(0);

    for (const vector of vectors) {
      const result = await runProbe("decode-frame", vector.wireHex);
      expect(result.exitCode, vector.name).toBe(0);
      expect(JSON.parse(result.stdout), vector.name).toEqual({
        kind: vector.kind,
        requestId: vector.requestId,
        streamId: vector.streamId,
        payloadBase64: vector.payloadBase64,
      });
    }
  });
});
