import { readdir, readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("protocol contract ownership", () => {
  it("treats the peer proto as the only wire contract source", async () => {
    expect((await readdir(`${root}/proto/liapoldus`)).sort()).toEqual(["peer"]);
    expect((await readdir(`${root}/proto/liapoldus/peer`)).sort()).toEqual(["v1"]);
    expect((await readdir(`${root}/proto/liapoldus/peer/v1`)).sort()).toEqual(["peer.proto"]);
  });

  it("keeps the peer contract free of services and product vocabulary", async () => {
    const proto = await readFile(`${root}/proto/liapoldus/peer/v1/peer.proto`, "utf8");
    expect(proto).toContain("package liapoldus.peer.v1");
    expect(proto).not.toMatch(/^service\s/m);
    expect(proto).not.toMatch(/capability|admin-ui|Reload|Rollback|Manifest/);
  });

  it("does not ship legacy embedded contract assets", async () => {
    const entries = new Set(await readdir(root));
    for (const stray of ["contracts", "pluginv1", "compatibility"]) {
      expect(entries.has(stray), `${stray}/ must not exist`).toBe(false);
    }
  });
});
