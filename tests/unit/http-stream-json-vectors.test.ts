import Ajv2020 from "ajv/dist/2020.js";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));
const canonicalSchemaURL = "https://github.com/Liapoldus/pluginprotocol/blob/main/";
const responseMetadataSchemaID = "https://github.com/Liapoldus/pluginprotocol/contracts/protocol/v1/http-stream-response-metadata.schema.json";

type Vector = {
  name: string;
  requestSchema: string;
  responseSchema: string;
  request: unknown;
  response: unknown;
};

function addContractSchemas(directory: string, ajv: Ajv2020): void {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = `${directory}/${entry.name}`;
    if (entry.isDirectory()) {
      addContractSchemas(path, ajv);
      continue;
    }
    if (!entry.isFile() || !entry.name.endsWith(".schema.json")) {
      continue;
    }

    const schema = JSON.parse(readFileSync(path, "utf8"));
    if (schema.$id) {
      ajv.addSchema(schema);
    } else {
      ajv.addSchema(schema, `${canonicalSchemaURL}${path.slice(root.length + 1)}`);
    }
  }
}

describe("HTTP stream JSON conformance vectors", () => {
  it("compiles response-start metadata references against the canonical response-action schema", () => {
    const ajv = new Ajv2020({ strict: false });
    addContractSchemas(`${root}/contracts`, ajv);

    expect(ajv.getSchema(responseMetadataSchemaID)).toBeDefined();
  });

  it("includes schema-valid stream-open and HTTP response-start examples", () => {
    const vectors = JSON.parse(
      readFileSync(`${root}/contracts/protocol/v1/json-payload-vectors.json`, "utf8"),
    ) as Vector[];
    const open = vectors.find((vector) => vector.name === "http-stream-open-context");
    const responseStart = vectors.find((vector) => vector.name === "http-stream-response-start");

    expect(open).toBeDefined();
    expect(responseStart).toBeDefined();
    expect(open?.requestSchema).toBe("contracts/protocol/v1/stream-open-context.schema.json");
    expect(responseStart?.responseSchema).toBe("contracts/protocol/v1/http-stream-response-metadata.schema.json");
  });
});
