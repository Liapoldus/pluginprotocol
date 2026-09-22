import { describe, expect, it } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("plugin v1 launch contract", () => {
  it("defines and exposes the loopback endpoint handoff used by Supervisor", () => {
    const contractPath = `${root}/contracts/protocol/v1/launch.json`;
    expect(existsSync(contractPath)).toBe(true);
    const contract = JSON.parse(readFileSync(contractPath, "utf8"));
    expect(contract.endpointEnvironment).toBe("LIAPOLDUS_PLUGIN_ENDPOINT");

    const source = readFileSync(`${root}/transport/server.go`, "utf8");
    expect(source).toContain("EndpointEnvironment");
    expect(source).toContain("ListenLoopback");
  });
});
