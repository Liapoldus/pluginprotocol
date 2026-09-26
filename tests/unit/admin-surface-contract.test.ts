import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("plugin admin surface contract", () => {
  it("defines a versioned declarative surface without executable UI payloads", async () => {
    const source = await readFile(`${root}/contracts/admin-ui/v1/schema.json`, "utf8");
    const schema = JSON.parse(source) as Record<string, unknown>;

    expect(schema).toMatchObject({ version: 1, kind: "liapoldus.plugin.admin-ui" });
    expect(JSON.stringify(schema)).toContain("pages");
    expect(JSON.stringify(schema)).toContain("actions");
    expect(JSON.stringify(schema)).not.toContain("javascript");
  });

  it("renders plugin settings from control RPCs without declaring a capability", async () => {
    const [uiSource, surfaceSource] = await Promise.all([
      readFile(`${root}/contracts/admin-ui/v1/schema.json`, "utf8"),
      readFile(`${root}/contracts/forms-db/v1/admin-surface.json`, "utf8"),
    ]);
    const ui = JSON.parse(uiSource) as {
      page: { source?: { exactlyOneOf?: string[][]; control?: { required?: string[]; properties?: Record<string, { const?: string }> } } };
      section: { properties?: { fieldsFromControlRpc?: { enum?: string[] } } };
    };
    const surface = JSON.parse(surfaceSource) as {
      requiredCapabilities: string[];
      pages: Array<Record<string, unknown>>;
    };
    const storage = surface.pages.find((page) => page.id === "storage");

    expect(ui.page.source?.exactlyOneOf).toEqual([["capability"], ["control"]]);
    expect(ui.page.source?.control?.required).toEqual(["settingsSchemaRpc", "settingsApplyRpc"]);
    expect(ui.page.source?.control?.properties).toEqual({
      settingsSchemaRpc: { const: "ConfigSchema" },
      settingsApplyRpc: { const: "ConfigApply" },
    });
    expect(ui.section.properties?.fieldsFromControlRpc?.enum).toEqual(["ConfigSchema"]);
    expect(storage).toMatchObject({
      control: { settingsSchemaRpc: "ConfigSchema", settingsApplyRpc: "ConfigApply" },
      sections: [{ id: "settings", kind: "form", fieldsFromControlRpc: "ConfigSchema" }],
    });
    expect(storage).not.toHaveProperty("capability");
    expect(surface.requiredCapabilities).not.toContain("config.schema");
  });
});
