import Ajv2020 from "ajv/dist/2020.js";
import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const readJSON = async (path: string) => JSON.parse(await readFile(join(root, path), "utf8"));

describe("plugin v1 launch settings schemas", () => {
  it("accepts absolute local binary paths, argument arrays, and only typed secret file references in env", async () => {
    const fileReference = await readJSON("contracts/protocol/v1/file-reference.schema.json");
    const launch = await readJSON("contracts/protocol/v1/local-launch.schema.json");
    const ajv = new Ajv2020({ strict: false });
    ajv.addSchema(fileReference);
    const validate = ajv.compile(launch);
    const valid = {
      binary: "/opt/liapoldus/plugins/forms",
      args: ["serve", "--config", "/etc/liapoldus/forms.json"],
      env: { FORMS_DB_PASSWORD: { kind: "file", path: "/run/secrets/forms-db-password" } },
    };

    expect(validate(valid)).toBe(true);
    expect(validate({ ...valid, binary: "plugins/forms" })).toBe(false);
    expect(validate({ ...valid, env: { FORMS_DB_PASSWORD: "plaintext" } })).toBe(false);
    expect(validate({ ...valid, env: { FORMS_DB_PASSWORD: { kind: "file", path: "secrets/password" } } })).toBe(false);
    expect(validate({ ...valid, unexpected: true })).toBe(false);
  });

  it("points remote mTLS credential fields at the common typed file-reference schema", async () => {
    const deployment = await readJSON("contracts/protocol/v1/remote-deployment.json");
    const tls = deployment.modes.remote.endpoint.properties.tls.properties;
    const expected = { $ref: "file-reference.schema.json" };

    expect(tls.ca).toEqual(expected);
    expect(tls.gatewayControlIdentity.properties.clientCertificate).toEqual(expected);
    expect(tls.gatewayControlIdentity.properties.clientKey).toMatchObject({ ...expected, minimumPermissions: "owner-read-only" });
    expect(tls.caddyDataIdentity.properties.clientCertificate).toEqual(expected);
    expect(tls.caddyDataIdentity.properties.clientKey).toMatchObject({ ...expected, minimumPermissions: "owner-read-only" });
  });
});
