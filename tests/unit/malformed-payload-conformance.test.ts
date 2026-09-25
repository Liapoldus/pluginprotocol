import Ajv2020 from "ajv/dist/2020.js";
import { readFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("malformed JSON and unsupported schema-version conformance", () => {
  it("rejects malformed JSON before schema validation", async () => {
    const vectors = JSON.parse(await readFile(`${root}/contracts/protocol/v1/malformed-payload-vectors.json`, "utf8"));

    expect(vectors.malformedJson.length).toBeGreaterThan(0);
    for (const vector of vectors.malformedJson) {
      await expect(readFile(`${root}/${vector.schema}`, "utf8"), vector.name).resolves.toBeTruthy();
      expect(() => JSON.parse(vector.raw), vector.name).toThrow();
    }
  });

  it("rejects a schema version derived from, but different from, the canonical schema version", async () => {
    const vectors = JSON.parse(await readFile(`${root}/contracts/protocol/v1/malformed-payload-vectors.json`, "utf8"));
    const schemaPaths = await allSchemaPaths(`${root}/contracts`);
    const ajv = new Ajv2020({ allErrors: true, strict: false });
    for (const schemaPath of schemaPaths) {
      const schema = JSON.parse(await readFile(schemaPath, "utf8"));
      ajv.addSchema(schema, schema.$id ?? `https://github.com/Liapoldus/pluginprotocol/blob/main/${schemaPath.slice(root.length + 1)}`);
    }

    expect(vectors.schemaVersionMismatch.length).toBeGreaterThan(0);
    for (const vector of vectors.schemaVersionMismatch) {
      const schemaPath = `${root}/${vector.schema}`;
      const schemaDocument = JSON.parse(await readFile(schemaPath, "utf8"));
      const schemaId = schemaDocument.$id ?? `https://github.com/Liapoldus/pluginprotocol/blob/main/${vector.schema}`;
      const validate = ajv.getSchema(schemaId);
      expect(validate, vector.name).toBeDefined();

      const payload = structuredClone(vector.payload) as Record<string, unknown>;
      const expectedVersion = findConst(schemaDocument, vector.versionProperty);
      expect(expectedVersion, vector.name).not.toBeUndefined();
      payload[vector.versionProperty] = "unsupported-schema-version";
      expect(payload[vector.versionProperty]).not.toEqual(expectedVersion);
      expect(validate!(payload), vector.name).toBe(false);
    }
  });
});

async function allSchemaPaths(directory: string): Promise<string[]> {
  const results: string[] = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = `${directory}/${entry.name}`;
    if (entry.isDirectory()) results.push(...await allSchemaPaths(path));
    else if (entry.isFile() && entry.name.endsWith(".schema.json")) results.push(path);
  }
  return results;
}

function findConst(value: unknown, property: string): unknown {
  if (Array.isArray(value)) {
    for (const item of value) {
      const match = findConst(item, property);
      if (match !== undefined) return match;
    }
    return undefined;
  }
  if (typeof value !== "object" || value === null) return undefined;
  const record = value as Record<string, unknown>;
  const properties = record.properties as Record<string, { const?: unknown }> | undefined;
  if (properties?.[property]?.const !== undefined) return properties[property].const;
  for (const child of Object.values(record)) {
    const match = findConst(child, property);
    if (match !== undefined) return match;
  }
  return undefined;
}
