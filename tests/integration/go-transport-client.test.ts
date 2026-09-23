import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../..", import.meta.url));
let plugin: ChildProcessWithoutNullStreams;
let address: string;

interface ClientResult {
  exitCode: number;
  stdout: string;
  stderr: string;
}

function runClientResult(endpoint: string, mode?: string): Promise<ClientResult> {
  return new Promise((resolve, reject) => {
    const args = ["run", "./tests/fixtures/grpc-client", endpoint];
    if (mode) args.push(mode);
    const process = spawn("go", args, { cwd: root });
    let stdout = "";
    let stderr = "";
    process.stdout.on("data", (chunk) => { stdout += String(chunk); });
    process.stderr.on("data", (chunk) => { stderr += String(chunk); });
    process.once("error", reject);
    process.once("exit", (code) => resolve({ exitCode: code ?? 1, stdout, stderr }));
  });
}

async function runClient(endpoint: string, mode?: string): Promise<string> {
  const result = await runClientResult(endpoint, mode);
  if (result.exitCode !== 0) throw new Error(result.stderr);
  return result.stdout;
}

async function startPlugin(environment: NodeJS.ProcessEnv = {}): Promise<{ process: ChildProcessWithoutNullStreams; address: string }> {
  const child = spawn("go", ["run", "./tests/fixtures/grpc-plugin"], { cwd: root, env: { ...globalThis.process.env, ...environment } });
  const lines = createInterface({ input: child.stdout });
  const address = await new Promise<string>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("plugin fixture did not become ready")), 30_000);
    lines.once("line", (line) => {
      clearTimeout(timeout);
      resolve(line);
    });
    child.once("error", reject);
    child.stderr.on("data", (data) => reject(new Error(String(data))));
    child.once("exit", (code) => reject(new Error(`plugin fixture exited (${code})`)));
  });
  return { process: child, address };
}

describe("public Go transport client", () => {
  beforeAll(async () => {
    const fixture = await startPlugin();
    plugin = fixture.process;
    address = fixture.address;
  }, 35_000);

  afterAll(() => plugin?.kill());

  it("performs handshake and JSON capability Call using the v1 transport API", async () => {
    const output = await runClient(address);
    expect(JSON.parse(output)).toEqual({ plugin: "fixture", response: { accepted: true } });
  });

  it("executes control handshake RPCs in manifest-schema-apply order", async () => {
    const directory = await mkdtemp(join(tmpdir(), "pluginprotocol-handshake-order-"));
    const trace = join(directory, "calls.log");
    const fixture = await startPlugin({ LIAPOLDUS_FIXTURE_TRACE: trace });
    try {
      const result = await runClient(fixture.address);
      expect(JSON.parse(result).plugin).toBe("fixture");
      expect(await readFile(trace, "utf8")).toBe("manifest\nconfig.schema\nconfig.apply\n");
    } finally {
      fixture.process.kill();
    }
  });

  it.each([
    ["name", ""],
    ["protocol version", "liapoldus.plugin.invalid"],
  ])("rejects an invalid manifest %s before requesting plugin configuration", async (_case, value) => {
    const directory = await mkdtemp(join(tmpdir(), "pluginprotocol-invalid-manifest-"));
    const trace = join(directory, "calls.log");
    const fixture = await startPlugin({
      LIAPOLDUS_FIXTURE_MANIFEST_NAME: value === "" ? "__empty__" : "fixture",
      LIAPOLDUS_FIXTURE_PROTOCOL_VERSION: value === "" ? "liapoldus.plugin.v1" : value,
      LIAPOLDUS_FIXTURE_TRACE: trace,
    });
    try {
      const result = await runClientResult(fixture.address);
      expect(result.exitCode).not.toBe(0);
      expect(result.stderr).toContain("plugin protocol violation");
      expect(await readFile(trace, "utf8")).toBe("manifest\n");
    } finally {
      fixture.process.kill();
    }
  });

  it("rejects a ConfigApply response that declines the runtime configuration", async () => {
    const directory = await mkdtemp(join(tmpdir(), "pluginprotocol-config-apply-rejected-"));
    const trace = join(directory, "calls.log");
    const fixture = await startPlugin({
      LIAPOLDUS_FIXTURE_CONFIG_APPLIED: "false",
      LIAPOLDUS_FIXTURE_TRACE: trace,
    });
    try {
      const result = await runClientResult(fixture.address);
      expect(result.exitCode).not.toBe(0);
      expect(result.stderr).toContain("plugin unavailable");
      expect(await readFile(trace, "utf8")).toBe("manifest\nconfig.schema\nconfig.apply\n");
    } finally {
      fixture.process.kill();
    }
  });

  it("classifies an oversized unary JSON payload as a protocol violation", async () => {
    const output = await runClient(address, "oversized");
    expect(JSON.parse(output)).toEqual({ error: "protocol_violation" });
  });
});
