import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("identity HTTP request JSON contract conformance", () => {
  it("accepts the bounded request shape serialized by the Gateway HTTP dispatcher", async () => {
    const vectors = JSON.parse(await readFile(`${root}/contracts/protocol/v1/json-payload-vectors.json`, "utf8")) as Array<Record<string, any>>;
    const vector = vectors.find(({ name }) => name === "identity-http-request-full-context");
    expect(vector).toBeDefined();
    expect(vector).toMatchObject({
      capability: "identity.client.callback",
      requestSchema: "contracts/identity/v1/http-request.schema.json",
      request: {
        method: "POST",
        path: "/identity/callback",
        query: "state=fixture",
        headers: { "content-type": "application/octet-stream", "x-request-mode": "interactive" },
        cookies: [{ name: "liap-session", value: "synthetic-inbound-value" }],
        body: "AAECAwQ=",
        requestId: "fixture-identity-request",
        remoteAddr: "203.0.113.7:43120",
        context: { tenant: "fixture-tenant" },
      },
    });

    const schema = JSON.parse(await readFile(`${root}/contracts/identity/v1/http-request.schema.json`, "utf8"));
    expect(schema.required).toEqual(expect.arrayContaining(["method", "path", "requestId"]));
    expect(schema.properties.headers).toMatchObject({ type: "object", maxProperties: 64 });
    expect(schema.properties.cookies).toMatchObject({ type: "array", maxItems: 32 });
    expect(schema.properties.body).toMatchObject({ type: "string", contentEncoding: "base64" });
    expect(schema.properties.requestId).toMatchObject({ type: "string", minLength: 1, maxLength: 256 });
  });
});
