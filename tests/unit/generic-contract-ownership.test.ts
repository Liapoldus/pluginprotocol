import { readdir, readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

async function files(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const nested = await Promise.all(entries.map(async (entry) => {
    const path = `${directory}/${entry.name}`;
    return entry.isDirectory() ? files(path) : [path];
  }));
  return nested.flat();
}

describe("protocol contract ownership", () => {
  it("keeps embedded JSON assets scoped to reusable protocol and shared HTTP contracts", async () => {
    const contractRoot = `${root.replace(/\/$/, "")}/contracts`;
    const contractFiles = (await files(contractRoot))
      .map((path) => path.slice(contractRoot.length + 1))
      .filter((path) => path.endsWith(".json"));
    expect(contractFiles.every((path) => path.startsWith("protocol/") || path.startsWith("http/") || path.startsWith("admin-ui/"))).toBe(true);
  });

  it("keeps generic conformance vectors independent of product capability schemas", async () => {
    const vectors = JSON.parse(await readFile(`${root}/contracts/protocol/v1/json-payload-vectors.json`, "utf8")) as Array<Record<string, unknown>>;
    for (const vector of vectors) {
      for (const field of ["requestSchema", "responseSchema"]) {
        const reference = vector[field];
        if (typeof reference === "string") {
          expect(reference.startsWith("contracts/protocol/") || reference.startsWith("contracts/http/") || reference.startsWith("contracts/admin-ui/")).toBe(true);
        }
      }
      expect(vector).not.toHaveProperty("capability");
    }
  });
});
