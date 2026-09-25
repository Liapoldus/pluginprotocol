import { describe, expect, it } from "vitest";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../..", import.meta.url));
const requestPath = `${root}/contracts/forms-db/v1/list-request.schema.json`;
const responsePath = `${root}/contracts/forms-db/v1/list-response.schema.json`;
const vectorsPath = `${root}/contracts/protocol/v1/json-payload-vectors.json`;

function compile(path: string) {
  const ajv = new Ajv2020({ allErrors: true, strict: false });
  addFormats(ajv);
  return ajv.compile(JSON.parse(readFileSync(path, "utf8")));
}

describe("forms.list v1 contract", () => {
  it("validates the documented request fields and optional filter/cursor", () => {
    const validate = compile(requestPath);

    expect(validate({ site: "portal", schemaName: "contact" })).toBe(true);
    expect(validate({
      site: "portal",
      schemaName: "contact",
      cursor: "opaque-cursor",
      limit: 50,
      filter: { field: "email", equals: "a@example.com" },
    })).toBe(true);
    expect(validate({ site: "portal", schemaName: "contact", limit: 0 })).toBe(false);
    expect(validate({ site: "portal", schemaName: "contact", limit: 101 })).toBe(false);
    expect(validate({ site: "portal", schemaName: "contact", unexpected: true })).toBe(false);
    expect(validate({ site: "portal", schemaName: "contact", filter: { field: "email" } })).toBe(false);
  });

  it("validates the existing full Submission list item and nullable nextCursor", () => {
    const validate = compile(responsePath);

    expect(validate({
      items: [{
        id: "frm_fixture",
        createdAt: "2026-01-01T00:00:00Z",
        site: "portal",
        schemaName: "contact",
        data: { email: "a@example.com" },
      }],
      nextCursor: null,
    })).toBe(true);
    expect(validate({ items: [], nextCursor: "opaque-cursor" })).toBe(true);
    expect(validate({ items: [], nextCursor: 1 })).toBe(false);
    expect(validate({ items: [{ id: "frm_fixture" }], nextCursor: null })).toBe(false);
    expect(validate({ items: [], nextCursor: null, unexpected: true })).toBe(false);
  });

  it("adds a forms.list request/response vector to the shared JSON conformance set", () => {
    const vectors = JSON.parse(readFileSync(vectorsPath, "utf8")) as Array<{ name: string; capability: string }>;
    expect(vectors).toContainEqual(expect.objectContaining({
      name: "forms-list-page",
      capability: "forms.list",
    }));
  });
});
