import { describe, expect, it } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("plugin v1 launch contract", () => {
  it("uses an inherited listener instead of endpoint environment variables", () => {
    const contractPath = `${root}/contracts/protocol/v1/launch.json`;
    expect(existsSync(contractPath)).toBe(true);
    const contract = JSON.parse(readFileSync(contractPath, "utf8"));
    expect(contract.listener).toMatchObject({ handoff: "inherited-file-descriptor", sdk: "transport.ListenInherited" });
    expect(contract).not.toHaveProperty("endpointEnvironment");
    expect(contract).not.toHaveProperty("grantBrokerEndpointEnvironment");

    const source = readFileSync(`${root}/transport/server.go`, "utf8");
    expect(source).toContain("ListenInherited");
    expect(source).not.toContain("EndpointEnvironment");
  });

  it("does not allow env, argv, or local application config files in local launch settings", () => {
    const schema = JSON.parse(readFileSync(`${root}/contracts/protocol/v1/local-launch.schema.json`, "utf8"));
    expect(schema.required).toEqual(["binary"]);
    expect(schema.properties).toHaveProperty("binary");
    expect(schema.properties).not.toHaveProperty("args");
    expect(schema.properties).not.toHaveProperty("env");
  });
});
