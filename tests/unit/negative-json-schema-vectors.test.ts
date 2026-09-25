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
  payload: Record<string, unknown>;
  mutation:
    | { kind: "removeRequired"; property: string }
    | { kind: "exceedStringMaximum"; property: string }
    | { kind: "exceedObjectPropertiesMaximum"; property: string }
    | { kind: "exceedArrayItemsMaximum"; property: string; item: unknown };
};

function streamContextLimit(property: string, keyword: "maxLength" | "maxProperties" | "maxItems"): number {
  const schema = JSON.parse(readFileSync(`${root}/contracts/protocol/v1/stream-open-context.schema.json`, "utf8"));
  const httpVariant = schema.oneOf.find((variant: { properties?: { kind?: { const?: string } } }) => variant.properties?.kind?.const === "http");
  const limit = httpVariant?.properties?.[property]?.[keyword];
  if (typeof limit !== "number") throw new Error(`missing ${keyword} for ${property}`);
  return limit;
}

function applyMutation(vector: NegativeVector): unknown {
  const payload = structuredClone(vector.payload);
  switch (vector.mutation.kind) {
    case "removeRequired":
      delete payload[vector.mutation.property];
      break;
    case "exceedStringMaximum":
      payload[vector.mutation.property] = "x".repeat(streamContextLimit(vector.mutation.property, "maxLength") + 1);
      break;
    case "exceedObjectPropertiesMaximum": {
      const properties = payload[vector.mutation.property] as Record<string, string>;
      const count = streamContextLimit(vector.mutation.property, "maxProperties") + 1;
      for (let index = 0; index < count; index += 1) properties[`x-header-${index}`] = "fixture";
      break;
    }
    case "exceedArrayItemsMaximum": {
      const items = payload[vector.mutation.property] as unknown[];
      const count = streamContextLimit(vector.mutation.property, "maxItems") + 1;
      while (items.length < count) items.push(vector.mutation.item);
      break;
    }
  }
  return payload;
}

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
      expect(validate!(applyMutation(vector)), vector.name).toBe(false);
    }
  });
});
