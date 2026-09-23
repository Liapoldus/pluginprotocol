import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { once } from "node:events";

const root = fileURLToPath(new URL("../..", import.meta.url));
let broker: ChildProcessWithoutNullStreams;
let address: string;

describe("typed scoped grant redemption", () => {
  beforeAll(async () => {
    broker = spawn("go", ["run", "./tests/fixtures/grant-broker"], { cwd: root });
    const lines = createInterface({ input: broker.stdout });
    const [line] = await Promise.race([
      once(lines, "line"),
      once(broker, "exit").then(([code]) => Promise.reject(new Error(`grant broker exited: ${code}`))),
    ]);
    address = String(line);
  }, 35_000);

  afterAll(() => broker?.kill());

  it("redeems through the typed RPC without printing returned secret bytes", async () => {
    const output = await new Promise<string>((resolve, reject) => {
      const process = spawn("go", ["run", "./tests/fixtures/grant-client", address], { cwd: root });
      let stdout = "";
      let stderr = "";
      process.stdout.on("data", (chunk) => { stdout += String(chunk); });
      process.stderr.on("data", (chunk) => { stderr += String(chunk); });
      process.once("error", reject);
      process.once("exit", (code) => code === 0 ? resolve(stdout) : reject(new Error(stderr)));
    });

    expect(JSON.parse(output)).toEqual({ redeemed: true });
    expect(output).not.toContain("fixture-secret");
  });
});
