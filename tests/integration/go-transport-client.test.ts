import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { once } from "node:events";

const root = fileURLToPath(new URL("../..", import.meta.url));
let plugin: ChildProcessWithoutNullStreams;
let address: string;

function runClient(endpoint: string, mode?: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const args = ["run", "./tests/fixtures/grpc-client", endpoint];
    if (mode) args.push(mode);
    const process = spawn("go", args, { cwd: root });
    let stdout = "";
    let stderr = "";
    process.stdout.on("data", (chunk) => { stdout += String(chunk); });
    process.stderr.on("data", (chunk) => { stderr += String(chunk); });
    process.once("error", reject);
    process.once("exit", (code) => code === 0 ? resolve(stdout) : reject(new Error(stderr)));
  });
}

describe("public Go transport client", () => {
  beforeAll(async () => {
    plugin = spawn("go", ["run", "./tests/fixtures/grpc-plugin"], { cwd: root });
    const lines = createInterface({ input: plugin.stdout });
    const [line] = await once(lines, "line");
    address = String(line);
  }, 35_000);

  afterAll(() => plugin?.kill());

  it("performs handshake and JSON capability Call using the v1 transport API", async () => {
    const output = await runClient(address);
    expect(JSON.parse(output)).toEqual({ plugin: "fixture", response: { accepted: true } });
  });

  it("classifies an oversized unary JSON payload as a protocol violation", async () => {
    const output = await runClient(address, "oversized");
    expect(JSON.parse(output)).toEqual({ error: "protocol_violation" });
  });
});
