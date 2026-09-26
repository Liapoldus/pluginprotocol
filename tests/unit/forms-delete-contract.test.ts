import Ajv2020 from "ajv/dist/2020.js";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));
const requestPath = `${root}/contracts/forms-db/v1/delete-request.schema.json`;
const responsePath = `${root}/contracts/forms-db/v1/delete-response.schema.json`;
const errorsPath = `${root}/contracts/forms-db/v1/delete-errors.json`;
const vectorsPath = `${root}/contracts/forms-db/v1/delete-negative-vectors.json`;

function schema(path: string) {
  return JSON.parse(readFileSync(path, "utf8"));
}

describe("forms.delete v1 contract", () => {
  it("validates the exact request and response shapes", () => {
    const ajv = new Ajv2020({ allErrors: true, strict: false });
    const request = ajv.compile(schema(requestPath));
    const response = ajv.compile(schema(responsePath));

    expect(request({ site: "portal", schemaName: "contact", id: "frm_123" })).toBe(true);
    expect(request({ site: "portal", schemaName: "contact" })).toBe(false);
    expect(request({ site: "portal", schemaName: "contact", id: "frm_123", extra: true })).toBe(false);
    expect(response({ deleted: true, id: "frm_123" })).toBe(true);
    expect(response({ deleted: false, id: "frm_123" })).toBe(false);
  });

  it("makes deletion of an absent record an explicit non-retryable not_found", () => {
    const errors = schema(errorsPath) as { missingRecord?: string; errors?: Record<string, { http: number; retryable: boolean }> };
    expect(errors.missingRecord).toBe("not_found");
    expect(errors.errors).toMatchObject({
      validation_failed: { http: 422, retryable: false },
      not_found: { http: 404, retryable: false },
      storage_unavailable: { http: 503, retryable: true },
    });
  });

  it("executes every negative request vector against its referenced schema", () => {
    const vectors = schema(vectorsPath) as Array<{ name: string; schema: string; payload: unknown }>;

    expect(vectors.length).toBeGreaterThanOrEqual(3);
    for (const vector of vectors) {
      const validate = new Ajv2020({ allErrors: true, strict: false }).compile(schema(`${root}/${vector.schema}`));
      expect(validate(vector.payload), vector.name).toBe(false);
    }
  });
});
