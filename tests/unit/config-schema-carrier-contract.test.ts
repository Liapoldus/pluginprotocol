import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("ConfigSchema nested JSON Schema carrier contract", () => {
  it("adds a bounded UTF-8 JSON Schema 2020-12 carrier without changing v1 field 1", async () => {
    const proto = await readFile(`${root}/proto/liapoldus/plugin/v1/control.proto`, "utf8");
    const contract = JSON.parse(await readFile(`${root}/contracts/protocol/v1/config-schema.json`, "utf8"));

    expect(proto).toMatch(/message ConfigSchema\s*\{[\s\S]*?repeated ConfigField fields\s*=\s*1;[\s\S]*?bytes json_schema\s*=\s*2;[\s\S]*?\}/);
    expect(contract).toMatchObject({
      protocolVersion: "liapoldus.plugin.v1",
      rpc: "PluginService.ConfigSchema",
      schemaDialect: "https://json-schema.org/draft/2020-12/schema",
      jsonSchema: {
        protobufField: "ConfigSchema.json_schema",
        fieldNumber: 2,
        encoding: "UTF-8 JSON bytes",
        maxBytes: 262144,
        references: "local fragment references only; network and external-file references are forbidden",
      },
      fields: {
        protobufField: "ConfigSchema.fields",
        fieldNumber: 1,
        role: "optional presentation hints; never a substitute for JSON Schema validation",
      },
      validation: {
        owner: "plugin",
        gatewayProductSchemaValidation: false,
        requiredWhen: "plugin accepts non-empty application settings through ConfigApply",
      },
    });
  });

  it("defines invalid schema and size conformance vectors", async () => {
    const contract = JSON.parse(await readFile(`${root}/contracts/protocol/v1/config-schema.json`, "utf8"));
    const vectors = JSON.parse(await readFile(`${root}/contracts/protocol/v1/config-schema-vectors.json`, "utf8"));

    expect(contract.rejection).toMatchObject({
      invalidJson: "INVALID_ARGUMENT",
      unsupportedDialect: "INVALID_ARGUMENT",
      remoteReference: "INVALID_ARGUMENT",
      overLimit: "RESOURCE_EXHAUSTED",
    });
    expect(vectors.positive.length).toBeGreaterThan(0);
    expect(vectors.negative.map((vector: { id: string }) => vector.id)).toEqual(expect.arrayContaining([
      "invalid-json",
      "unsupported-dialect",
      "remote-reference",
      "schema-over-limit",
    ]));
  });
});
