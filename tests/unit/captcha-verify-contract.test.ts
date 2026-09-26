import Ajv2020 from "ajv/dist/2020.js";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));
const requestPath = `${root}/contracts/captcha/v1/verify-request.schema.json`;
const responsePath = `${root}/contracts/captcha/v1/verify-response.schema.json`;
const errorsPath = `${root}/contracts/captcha/v1/verify-errors.json`;
const vectorsPath = `${root}/contracts/captcha/v1/negative-vectors.json`;

function schema(path: string) {
  return JSON.parse(readFileSync(path, "utf8"));
}

describe("captcha.verify v1 contract", () => {
  it("validates payload and verification result without accepting client-selected providers", () => {
    const ajv = new Ajv2020({ allErrors: true, strict: false });
    const request = ajv.compile(schema(requestPath));
    const response = ajv.compile(schema(responsePath));

    expect(request({ token: "synthetic-captcha-token" })).toBe(true);
    expect(request({ token: "synthetic-captcha-token", provider: "attacker-selected" })).toBe(false);
    expect(response({ valid: true })).toBe(true);
    expect(response({ valid: false, providerCode: "invalid_token" })).toBe(true);
    expect(response({ valid: "true" })).toBe(false);
  });

  it("defines stable validation and retryable provider failures", () => {
    const errors = schema(errorsPath) as { errors?: Record<string, { http: number; retryable: boolean }> };
    expect(errors.errors).toEqual({
      validation_failed: { http: 422, retryable: false },
      provider_unavailable: { http: 503, retryable: true },
    });
  });

  it("executes every negative request vector against the v1 schema", () => {
    const vectors = schema(vectorsPath) as Array<{ name: string; schema: string; payload: unknown }>;

    expect(vectors.length).toBeGreaterThanOrEqual(3);
    for (const vector of vectors) {
      const validate = new Ajv2020({ allErrors: true, strict: false }).compile(schema(`${root}/${vector.schema}`));
      expect(validate(vector.payload), vector.name).toBe(false);
    }
  });
});
