import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { readFile } from "node:fs/promises";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { once } from "node:events";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";

const root = fileURLToPath(new URL("../..", import.meta.url));
type GrantVector = {
  name: string;
  request: { handle: string; purpose: string; domain: string; capability: string };
  expectedClientResult: "redeemed" | "rejected";
};
let broker: ChildProcessWithoutNullStreams;
let brokerFixture: GoFixtureBinary | undefined;
let clientFixture: GoFixtureBinary | undefined;
let address: string;

async function runVector(vector: GrantVector): Promise<{ result: string }> {
  return new Promise((resolve, reject) => {
    const child = spawn(clientFixture!.executable, [address, JSON.stringify(vector.request)], { cwd: root });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => { stdout += String(chunk); });
    child.stderr.on("data", (chunk) => { stderr += String(chunk); });
    child.once("error", reject);
    child.once("exit", (code) => {
      if (code !== 0) return reject(new Error(stderr));
      try { resolve(JSON.parse(stdout) as { result: string }); }
      catch { reject(new Error("grant fixture returned invalid result")); }
    });
  });
}

describe("GrantBroker vector behavior", () => {
  beforeAll(async () => {
    brokerFixture = await buildGoFixture(root, "./tests/fixtures/grant-broker");
    clientFixture = await buildGoFixture(root, "./tests/fixtures/grant-client");
    broker = startGoFixture(brokerFixture.executable, { cwd: root });
    const lines = createInterface({ input: broker.stdout });
    const [line] = await Promise.race([
      once(lines, "line"),
      once(broker, "exit").then(([code]) => Promise.reject(new Error(`grant broker exited: ${code}`))),
    ]);
    address = String(line);
  }, 35_000);

  afterAll(async () => {
    if (broker) await stopChildProcess(broker);
    await brokerFixture?.cleanup();
    await clientFixture?.cleanup();
  });

  it("applies capability, purpose, domain, and required-field authorization vectors", async () => {
    const vectors = JSON.parse(
      await readFile(`${root}/contracts/protocol/v1/grant-redemption-vectors.json`, "utf8"),
    ) as GrantVector[];

    for (const vector of vectors) {
      const result = await runVector(vector);
      expect(result.result, vector.name).toBe(vector.expectedClientResult);
    }
  });
});
