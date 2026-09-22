import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("test-only generated gRPC TypeScript client", () => {
  it("exposes all v1 service methods from the normative proto", async () => {
    const source = await readFile(`${root}/tests/generated/liapoldus/plugin/v1/service.ts`, "utf8");

    expect(source).toContain("PluginServiceClient");
    expect(source).toContain("PluginServiceService");
    expect(source).toContain("/liapoldus.plugin.v1.PluginService/Manifest");
    expect(source).toContain("/liapoldus.plugin.v1.PluginService/ConfigApply");
    expect(source).toContain("/liapoldus.plugin.v1.PluginService/Call");
    expect(source).toContain("/liapoldus.plugin.v1.PluginService/Stream");
  });
});
