import { describe, expect, it } from "vitest";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../..", import.meta.url));
const vectorPath = `${root}/contracts/protocol/v1/json-payload-vectors.json`;

type PayloadVector = {
  name: string;
  capability: string;
  requestSchema: string;
  responseSchema: string;
  request: unknown;
  response: unknown;
};

function loadSchemas(directory: string, ajv: Ajv2020): void {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const entryPath = `${directory}/${entry.name}`;
    if (entry.isDirectory()) {
      loadSchemas(entryPath, ajv);
      continue;
    }
    if (!entry.isFile() || !entry.name.endsWith(".schema.json")) {
      continue;
    }

    const schema = JSON.parse(readFileSync(entryPath, "utf8"));
    if (schema.$id) {
      ajv.addSchema(schema);
      continue;
    }

    const relativePath = entryPath.slice(root.length + 1);
    ajv.addSchema(schema, `https://github.com/Liapoldus/pluginprotocol/blob/main/${relativePath}`);
  }
}

function validateVectors(vectors: PayloadVector[]): string[] {
  const ajv = new Ajv2020({ allErrors: true, strict: false });
  addFormats(ajv);
  loadSchemas(`${root}/contracts`, ajv);

  const errors: string[] = [];
  for (const vector of vectors) {
    for (const [direction, schemaRef, payload] of [
      ["request", vector.requestSchema, vector.request],
      ["response", vector.responseSchema, vector.response],
    ] as const) {
      const schemaFile = `${root}/${schemaRef}`;
      let schemaDocument: { $id?: string };
      try {
        schemaDocument = JSON.parse(readFileSync(schemaFile, "utf8"));
      } catch {
        errors.push(`${vector.name}: unresolved ${direction} schema ${schemaRef}`);
        continue;
      }
      const validator = schemaDocument.$id
        ? ajv.getSchema(schemaDocument.$id)
        : ajv.getSchema(`https://github.com/Liapoldus/pluginprotocol/blob/main/${schemaRef}`);
      if (!validator) {
        errors.push(`${vector.name}: unresolved ${direction} schema ${schemaRef}`);
        continue;
      }
      if (!validator(payload)) {
        errors.push(`${vector.name}: invalid ${direction}: ${ajv.errorsText(validator.errors)}`);
      }
    }
  }
  return errors;
}

describe("versioned JSON payload conformance vectors", () => {
  it("references resolvable JSON Schemas and validates every request and response", () => {
    const vectors = JSON.parse(readFileSync(vectorPath, "utf8")) as PayloadVector[];

    expect(vectors.length).toBeGreaterThan(0);
    expect(validateVectors(vectors)).toEqual([]);
  });

  it("rejects a vector whose payload violates its referenced schema", () => {
    const vectors = JSON.parse(readFileSync(vectorPath, "utf8")) as PayloadVector[];
    const vector = structuredClone(vectors[0]);
    vector.response = { status: 99, headers: { "Set-Cookie": "must-not-pass" } };

    expect(validateVectors([vector])).not.toEqual([]);
  });

  it("keeps obsolete wire-hex data out of vectors", () => {
    const vectors = readFileSync(vectorPath, "utf8");
    expect(vectors).not.toMatch(/\bwireHex\b/i);
  });
});
