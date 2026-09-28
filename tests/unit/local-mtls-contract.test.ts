import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("local workload mTLS contract", () => {
  it("binds local channels to launch-scoped pins without a CA or private-key exchange", async () => {
    const contract = JSON.parse(await readFile(`${root}/contracts/protocol/v1/local-workload-mtls.json`, "utf8"));
    expect(contract).toMatchObject({
      protocolVersion: "liapoldus.plugin.v1",
      mode: "supervised-local",
      bootstrap: {
        channel: "private-inherited-pipe",
        directions: { gatewayToPluginDescriptor: 4, pluginToGatewayDescriptor: 5 },
        framing: "newline-delimited-json",
        messageSchema: "local-bootstrap.schema.json",
        maximumMessageBytes: 16384,
        challengeBytes: 32,
        sendsPrivateKeys: false,
        refreshPinsOnEveryLaunch: true,
      },
      identity: { form: "uri-san", comparison: "exact" },
      security: {
        minimumTLSVersion: 772,
        mutualAuthentication: true,
        clientCertificateRequired: true,
        certificateTrust: "exact-sha256-leaf-pin",
        certificateAuthorityRequired: false,
        insecureFallback: false,
        pinInput: "leaf-certificate-der",
        pinComparison: "constant-time",
      },
    });

    const source = await readFile(`${root}/infrastructure/transport/local_tls.go`, "utf8");
    expect(source).toContain("contracts/protocol/v1/local-workload-mtls.json");
    expect(source).toContain("contract.Security.MinimumTLSVersion");
    expect(source).toContain("contract.Security.ClientCertificateRequired");
    expect(source).toContain("contract.Security.CertificateAuthorityRequired");
  });

  it("validates the offer, response, and confirmation messages and rejects private-key fields", async () => {
    const schema = JSON.parse(await readFile(`${root}/contracts/protocol/v1/local-bootstrap.schema.json`, "utf8"));
    const ajv = new Ajv2020({ allErrors: true, strict: false });
    addFormats(ajv);
    const validate = ajv.compile(schema);
    const challenge = `${"A".repeat(43)}=`;
    const gatewayIdentity = "urn:liapoldus:gateway:one";
    const pluginIdentity = "urn:liapoldus:plugin:one";
    const offer = {
      protocolVersion: "liapoldus.plugin.v1", gatewayIdentity, pluginIdentity,
      gatewayChallenge: challenge, gatewayCertificate: "AQ==",
    };
    const response = {
      protocolVersion: "liapoldus.plugin.v1", gatewayIdentity, pluginIdentity,
      gatewayChallenge: challenge, pluginChallenge: challenge, pluginCertificate: "AQ==",
    };
    const confirmation = {
      protocolVersion: "liapoldus.plugin.v1", gatewayIdentity, pluginIdentity,
      gatewayChallenge: challenge, pluginChallenge: challenge,
    };

    expect(validate(offer)).toBe(true);
    expect(validate(response)).toBe(true);
    expect(validate(confirmation)).toBe(true);
    expect(validate({ ...offer, privateKey: "must-never-cross-the-pipe" })).toBe(false);
    expect(validate({ ...offer, gatewayChallenge: "short" })).toBe(false);
  });
});
