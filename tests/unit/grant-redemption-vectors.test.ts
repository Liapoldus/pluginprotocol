import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

type GrantVector = {
  name: string;
  request: { handle: string; purpose: string; domain: string; capability: string };
  expectedClientResult: "redeemed" | "rejected";
};

describe("GrantBroker redemption conformance vectors", () => {
  it("covers allowed, denied-scope, and locally malformed requests without embedding secrets", async () => {
    const vectors = JSON.parse(
      await readFile(`${root}/contracts/protocol/v1/grant-redemption-vectors.json`, "utf8"),
    ) as GrantVector[];

    expect(vectors.map(({ name }) => name)).toEqual([
      "authorized-redemption",
      "capability-scope-denied",
      "purpose-scope-denied",
      "domain-scope-denied",
      "missing-handle-rejected-locally",
      "missing-purpose-rejected-locally",
      "missing-capability-rejected-locally",
    ]);
    expect(vectors[0]?.expectedClientResult).toBe("redeemed");
    expect(vectors.slice(1).every((vector) => vector.expectedClientResult === "rejected")).toBe(true);
    expect(JSON.stringify(vectors)).not.toMatch(/fixture-secret|secretMaterial|secretBytes/i);
  });
});
