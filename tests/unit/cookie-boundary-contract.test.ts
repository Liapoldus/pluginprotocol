import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";
import { describe, expect, it } from "vitest";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const json = async (path: string) => JSON.parse(await readFile(join(root, path), "utf8"));

describe("plugin HTTP cookie boundary v1", () => {
  it("defines an incoming allow-list scoped to one plugin instance and capability", async () => {
    const schema = await json("contracts/http/v1/cookie-policy.schema.json");

    expect(schema.required).toEqual(expect.arrayContaining(["version", "instanceId", "capability", "allowedNames"]));
    expect(schema.properties.version.const).toBe(1);
    expect(schema.properties.allowedNames.uniqueItems).toBe(true);
    expect(schema["x-liapoldus-semantics"].scope).toEqual(["instanceId", "capability"]);
    expect(schema["x-liapoldus-semantics"].forwarding).toMatch(/only.*allow-listed/i);

    const validate = new Ajv2020({ allErrors: true, strict: false }).compile(schema);
    expect(validate({ version: 1, instanceId: "identity-main", capability: "identity.client.callback", allowedNames: ["liap-session"] })).toBe(true);
    expect(validate({ version: 1, instanceId: "identity-main", capability: "identity.client.callback", allowedNames: ["liap-session", "liap-session"] })).toBe(false);
    expect(validate({ version: 1, instanceId: "identity-main", capability: "identity.client.callback", allowedNames: ["*"] })).toBe(false);
  });

  it("specifies filtering, atomic rejection, and redaction behavior without logging raw values", async () => {
    const boundary = await json("contracts/http/v1/cookie-boundary.json");

    expect(boundary.version).toBe(1);
    expect(boundary.incoming.policyScope).toEqual(["instanceId", "capability"]);
    expect(boundary.incoming.unlistedCookie).toBe("omit");
    expect(boundary.outgoing.invalidAction).toBe("reject-entire-response-before-commit");
    expect(boundary.security.redactValue).toEqual(["logs", "traces", "audit", "errors"]);
    expect(boundary.errors.invalidCookiePolicy).toBeDefined();
    expect(boundary.errors.invalidOutgoingAction).toBeDefined();
  });

  it("publishes machine-readable response cookie semantic constraints", async () => {
    const schema = await json("contracts/http/v1/response-action.schema.json");
    const constraints = schema["x-liapoldus-semantics"].cookieConstraints;

    expect(constraints.sameSiteRequiresSecure).toBe("None");
    expect(constraints.securePrefixes).toContain("__Secure-");
    expect(constraints.hostPrefix).toEqual({ prefix: "__Host-", secure: true, path: "/", domainAllowed: false });
    expect(constraints.domain).toEqual({ mustDomainMatchRequestHost: true, rejectPublicSuffix: true });
  });

  it("reuses shared cookie name/value constraints for every inbound HTTP mode", async () => {
    const stream = await json("contracts/protocol/v1/stream-open-context.schema.json");
    const cookieBranches = stream.oneOf.filter((branch: any) => branch.properties?.cookies);

    expect(cookieBranches).toHaveLength(3);
    for (const branch of cookieBranches) {
      expect(branch.properties.cookies.items.$ref).toBe(
        "https://github.com/Liapoldus/pluginprotocol/contracts/http/v1/cookie-pair.schema.json#/$defs/cookiePair",
      );
    }
  });

  it("validates Set-Cookie values and paths according to the Go HTTP cookie serializer", async () => {
    const response = await json("contracts/http/v1/response-action.schema.json");
    const ajv = new Ajv2020({ allErrors: true, strict: false });
    addFormats(ajv);
    const validate = ajv.compile(response);
    const action = (value: string, path: string) => ({
      status: 200,
      cookies: [{ name: "session", value, path, secure: true, httpOnly: true }],
    });

    expect(validate(action("space and,comma", "/valid path"))).toBe(true);
    expect(validate(action("semi;colon", "/valid"))).toBe(false);
    expect(validate(action("valid", "/semi;path"))).toBe(false);
    expect(validate(action("non-ascii-ø", "/valid"))).toBe(false);
  });

  it("conforms stream request cookies and ordinary/HttpOnly response actions", async () => {
    const vectors = await json("contracts/protocol/v1/json-payload-vectors.json");
    const names = vectors.map((vector: { name: string }) => vector.name);
    expect(names).toContain("http-stream-open-context-cookie");
    expect(names).toContain("http-response-cookie-ordinary");
    expect(names).toContain("http-response-cookie-httponly");
  });
});
