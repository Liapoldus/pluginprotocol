import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("pluginprotocol gRPC v1 API", () => {
  it("defines the typed control RPCs and generic capabilities without a custom health RPC", async () => {
    const service = await readFile(`${root}/proto/liapoldus/plugin/v1/service.proto`, "utf8");
    const control = await readFile(`${root}/proto/liapoldus/plugin/v1/control.proto`, "utf8");

    expect(service).toContain("package liapoldus.plugin.v1;");
    expect(service).toContain("service PluginService");
    expect(service).toMatch(/rpc Manifest\(ManifestRequest\) returns \(Manifest\)/);
    expect(service).toMatch(/rpc ConfigSchema\(ConfigSchemaRequest\) returns \(ConfigSchema\)/);
    expect(service).toMatch(/rpc ConfigApply\(ConfigApplyRequest\) returns \(ConfigApplyResult\)/);
    expect(service).toMatch(/rpc Shutdown\(ShutdownRequest\) returns \(ShutdownResult\)/);
    expect(service).toMatch(/rpc Call\(CallRequest\) returns \(CallResponse\)/);
    expect(service).toMatch(/rpc Stream\(stream StreamMessage\) returns \(stream StreamMessage\)/);
    expect(service).not.toMatch(/rpc Health\(/);
    expect(control).toContain("message ManifestRequest");
    expect(control).toContain("message ConfigSchemaRequest");
    expect(control).toContain("message ConfigApplyRequest");
    expect(control).toContain("message ShutdownRequest");
  });
});
