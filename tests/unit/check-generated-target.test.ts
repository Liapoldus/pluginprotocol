import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("generated source check target", () => {
  it("regenerates the peer wire into a temporary directory and compares it", () => {
    const makefile = readFileSync(`${root}/Makefile`, "utf8");
    const target = makefile.match(/^check-generated:([^\n]*)\n((?:\t[^\n]*\n?)*)/m);

    expect(target, "check-generated target must exist").not.toBeNull();
    expect(target?.[1].trim(), "the target must not run mutating generate targets").toBe("");
    expect(target?.[2]).toContain("mktemp -d");
    expect(target?.[2]).toContain("find . -type f");
    expect(target?.[2]).toContain("cmp ");
    expect(target?.[2]).not.toContain("git diff");
    expect(target?.[2]).toMatch(/--go_out=.*tmp_dir/);
    expect(target?.[2]).toContain("$(PEER_PROTOS)");
    expect(makefile).toContain("PEER_PROTOS := liapoldus/peer/v1/peer.proto");
  });

  it("generates Go only for the generic peer contract", () => {
    const makefile = readFileSync(`${root}/Makefile`, "utf8");

    expect(makefile).not.toContain("liapoldus/plugin");
    expect(makefile).not.toContain("--ts_proto_out");
    expect(makefile).not.toContain("--go-grpc_out");
    expect(makefile).not.toMatch(/generate-ts/);
    expect(makefile).not.toContain("protoc-gen-ts_proto");
  });

  it("compiles every package without writing a binary into the working tree", () => {
    // A fixture is a main package, so a bare "go build ./..." drops a fixture
    // binary in the repository root. It is untracked, so a later "git add -A" would
    // commit an 11 MB executable, and it is a mutating side effect in a gate that is
    // supposed to be read-only.
    const makefile = readFileSync(`${root}/Makefile`, "utf8");

    expect(makefile).toContain("go build -o /dev/null ./...");
    expect(makefile).not.toMatch(/^\tgo build \.\/\.\.\.$/m);
  });
});
