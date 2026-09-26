import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("pluginprotocol gRPC v1 API", () => {
  it("defines the typed control RPCs and generic capabilities without a custom health RPC", async () => {
    const service = await readFile(`${root}/proto/liapoldus/plugin/v1/service.proto`, "utf8");
    const control = await readFile(`${root}/proto/liapoldus/plugin/v1/control.proto`, "utf8");
    const grant = await readFile(`${root}/proto/liapoldus/plugin/v1/grant.proto`, "utf8");

    expect(service).toContain("package liapoldus.plugin.v1;");
    expect(service).toContain("service PluginService");
    expect(service).toMatch(/rpc Manifest\(ManifestRequest\) returns \((?:\.liapoldus\.plugin\.v1\.)?Manifest\)/);
    expect(service).toMatch(/rpc Bootstrap\(BootstrapRequest\) returns \(BootstrapResult\)/);
    expect(service).toMatch(/rpc ConfigSchema\(ConfigSchemaRequest\) returns \((?:\.liapoldus\.plugin\.v1\.)?ConfigSchema\)/);
    expect(service).toMatch(/rpc ConfigApply\(ConfigApplyRequest\) returns \(ConfigApplyResult\)/);
    expect(service).toMatch(/rpc Shutdown\(ShutdownRequest\) returns \(ShutdownResult\)/);
    expect(service).toMatch(/rpc Call\(CallRequest\) returns \(CallResponse\)/);
    expect(service).toMatch(/rpc Stream\(stream StreamMessage\) returns \(stream StreamMessage\)/);
    expect(service).not.toMatch(/rpc Health\(/);
    expect(control).toContain("message ManifestRequest");
    expect(control).toContain("message BootstrapRequest");
    expect(control).toContain("message BootstrapResult");
    expect(control).toContain("message ConfigSchemaRequest");
    expect(control).toContain("message ConfigApplyRequest");
    expect(control).toContain("settings_revision");
    expect(control).toContain("repeated ActiveGrant grants");
    expect(grant).toContain("GRANT_SCOPE_CONFIG_APPLY");
    expect(grant).toContain("secret_reference");
    expect(control).toContain("message ShutdownRequest");
  });

  it("keeps bootstrap operational-only and makes config delivery a Gateway-pushed RPC", async () => {
    const control = await readFile(`${root}/proto/liapoldus/plugin/v1/control.proto`, "utf8");
    const service = await readFile(`${root}/proto/liapoldus/plugin/v1/service.proto`, "utf8");
    const configApply = service.indexOf("rpc ConfigApply(");

    expect(control).toMatch(/message BootstrapRequest\s*\{[^}]*instance_id[^}]*grant_broker_endpoint/s);
    expect(control).not.toMatch(/message BootstrapRequest\s*\{[^}]*config|message BootstrapRequest\s*\{[^}]*secret/s);
    expect(service).toMatch(/rpc ConfigApply\(ConfigApplyRequest\) returns \(ConfigApplyResult\)/);
    expect(configApply).toBeGreaterThan(service.indexOf("service PluginService"));
    expect(service).not.toMatch(/rpc (?:GetConfig|FetchConfig|PullConfig)\(/);
  });
});
