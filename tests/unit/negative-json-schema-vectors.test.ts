import Ajv2020 from "ajv/dist/2020.js";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));
const vectorsPath = `${root}/contracts/protocol/v1/negative-json-schema-vectors.json`;
const contextSchemaId = "https://github.com/Liapoldus/pluginprotocol/blob/main/contracts/protocol/v1/stream-open-context.schema.json";

type NegativeVector = {
  name: string;
  schema: string;
  payload: unknown;
};

function loadSchemas(directory: string, ajv: Ajv2020): void {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = `${directory}/${entry.name}`;
    if (entry.isDirectory()) {
      loadSchemas(path, ajv);
      continue;
    }
    if (!entry.isFile() || !entry.name.endsWith(".schema.json")) continue;

    const schema = JSON.parse(readFileSync(path, "utf8"));
    const id = schema.$id ?? `https://github.com/Liapoldus/pluginprotocol/blob/main/${path.slice(root.length + 1)}`;
    ajv.addSchema(schema, id);
  }
}

describe("negative JSON Schema conformance vectors", () => {
  it("rejects malformed and out-of-bounds HTTP stream open contexts", () => {
    const vectors = JSON.parse(readFileSync(vectorsPath, "utf8")) as NegativeVector[];
    const ajv = new Ajv2020({ allErrors: true, strict: false });
    loadSchemas(`${root}/contracts`, ajv);
    const validate = ajv.getSchema(contextSchemaId);

    expect(validate).toBeDefined();
    expect(vectors.map(({ name }) => name)).toEqual([
      "http-stream-context-missing-request-id",
      "http-stream-context-path-over-maximum",
      "http-stream-context-too-many-headers",
      "http-stream-context-too-many-cookies",
    ]);

    for (const vector of vectors) {
      expect(vector.schema, vector.name).toBe("contracts/protocol/v1/stream-open-context.schema.json");
      expect(validate!(vector.payload), vector.name).toBe(false);
    }
  });
});
