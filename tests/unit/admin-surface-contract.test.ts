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
});
