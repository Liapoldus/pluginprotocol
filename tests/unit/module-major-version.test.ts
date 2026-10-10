import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("Go module major version", () => {
  it("separates the breaking Go API from the stable peer.v1 wire contract", async () => {
    const goMod = await readFile(`${root}/go.mod`, "utf8");
    const makefile = await readFile(`${root}/Makefile`, "utf8");
    const proto = await readFile(`${root}/proto/liapoldus/peer/v1/peer.proto`, "utf8");
    const readme = await readFile(`${root}/README.md`, "utf8");

    expect(goMod).toMatch(/^module github\.com\/Liapoldus\/pluginprotocol\/v3$/m);
    expect(makefile).toContain("MODULE := github.com/Liapoldus/pluginprotocol/v3");
    expect(proto).toContain("github.com/Liapoldus/pluginprotocol/v3/infrastructure/peer/wire;wire");
    expect(proto).toContain("package liapoldus.peer.v1;");
    expect(readme).toContain("module major `v3`");
    expect(readme).toContain("liapoldus.peer.v1");
  });
});
