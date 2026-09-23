import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const json = async (path: string) => JSON.parse(await readFile(join(root, path), "utf8"));

describe("protocol v1 remote deployment and HTTP cookie contracts", () => {
  it("defines a fixed remote TLS/mTLS endpoint without insecure fallback", async () => {
    const deployment = await json("contracts/protocol/v1/remote-deployment.json");
    expect(deployment.protocolVersion).toBe("liapoldus.plugin.v1");
    expect(deployment.modes.remote.endpoint.required).toEqual(expect.arrayContaining(["address", "tls"]));
    expect(deployment.modes.remote.endpoint.properties.tls.required).toEqual(
      expect.arrayContaining(["serverName", "ca", "clientCertificate", "clientKey"]),
    );
    expect(deployment.modes.remote.fallback).toBe("none");
    expect(deployment.modes.remote.processOwner).toBe("external");
  });

  it("shares a versioned typed cookie response-action schema", async () => {
    const schema = await json("contracts/http/v1/response-action.schema.json");
    expect(schema.properties.cookies.maxItems).toBe(32);
    expect(schema.properties.cookies.items.$ref).toBe("#/$defs/cookieAction");
    expect(schema.$defs.cookieAction.required).toEqual(
      expect.arrayContaining(["name", "value", "secure", "httpOnly"]),
    );
    expect(schema.$defs.cookieAction.properties.httpOnly.type).toBe("boolean");
    expect(schema.$defs.cookieAction.properties.sameSite.enum).toEqual(["Strict", "Lax", "None"]);
  });

  it("composes identity HTTP actions from the shared cookie boundary", async () => {
    const schema = await json("contracts/identity/v1/http-actions.schema.json");
    expect(schema.allOf[0].$ref).toBe("../../http/v1/response-action.schema.json");
    expect(schema.allOf[1].properties.identity.type).toBe("object");
  });
});
