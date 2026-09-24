import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("generated source check target", () => {
  it("regenerates into a temporary directory and compares against the working tree", () => {
    const makefile = readFileSync(`${root}/Makefile`, "utf8");
    const target = makefile.match(/^check-generated:([^\n]*)\n((?:\t[^\n]*\n?)*)/m);

    expect(target, "check-generated target must exist").not.toBeNull();
    expect(target?.[1].trim(), "the target must not run mutating generate targets").toBe("");
    expect(target?.[2]).toContain("mktemp -d");
    expect(target?.[2]).toContain("find . -type f");
    expect(target?.[2]).toContain("cmp ");
    expect(target?.[2]).not.toContain("git diff");
    expect(target?.[2]).toMatch(/--go_out=.*tmp_dir/);
    expect(target?.[2]).toMatch(/--ts_proto_out=.*tmp_dir/);
  });
});
