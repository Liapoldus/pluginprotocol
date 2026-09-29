import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
let fixture: GoFixtureBinary | undefined;

async function startPlugin(mode: string): Promise<{ process: ChildProcessWithoutNullStreams; endpoint: string }> {
  if (!fixture) throw new Error("ConfigApply fixture binary is not built");
  const process = startGoFixture(fixture.executable, { cwd: root }, ["serve", mode]);
  const lines = createInterface({ input: process.stdout });
  const endpoint = await new Promise<string>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("ConfigApply fixture did not become ready")), 30_000);
    lines.once("line", (line) => {
      clearTimeout(timeout);
      resolve(line);
    });
    process.once("error", reject);
    process.stderr.on("data", (data) => reject(new Error(String(data))));
    process.once("exit", (code) => reject(new Error(`ConfigApply fixture exited (${code})`)));
  });
  return { process, endpoint };
}

function runApply(endpoint: string): Promise<{ exitCode: number; stdout: string; stderr: string }> {
  if (!fixture) throw new Error("ConfigApply fixture binary is not built");
  return new Promise((resolve, reject) => {
    const process = spawn(fixture!.executable, ["apply", endpoint], { cwd: root });
    let stdout = "";
    let stderr = "";
    process.stdout.on("data", (chunk) => { stdout += String(chunk); });
    process.stderr.on("data", (chunk) => { stderr += String(chunk); });
    process.once("error", reject);
    process.once("exit", (code) => resolve({ exitCode: code ?? 1, stdout, stderr }));
  });
}

describe("public SDK ConfigApply", () => {
  beforeAll(async () => {
    fixture = await buildGoFixture(root, "./tests/fixtures/sdk-config-apply");
  }, 35_000);

  afterAll(async () => {
    await fixture?.cleanup();
  });

  it("pushes an accepted settings revision", async () => {
    const plugin = await startPlugin("accepted");
    try {
      const result = await runApply(plugin.endpoint);
      expect(result.exitCode).toBe(0);
      expect(JSON.parse(result.stdout)).toEqual({ result: "applied" });
    } finally {
      await stopChildProcess(plugin.process);
    }
  });

  it("returns a stable typed error when the plugin rejects the revision", async () => {
    const plugin = await startPlugin("rejected");
    try {
      const result = await runApply(plugin.endpoint);
      expect(result.exitCode).toBe(0);
      expect(JSON.parse(result.stdout)).toEqual({ result: "config_apply_rejected" });
    } finally {
      await stopChildProcess(plugin.process);
    }
  });

  it("classifies the readiness wrapper's failed-precondition revision mismatch", async () => {
    const plugin = await startPlugin("mismatched-revision");
    try {
      const result = await runApply(plugin.endpoint);
      expect(result.exitCode).toBe(0);
      expect(JSON.parse(result.stdout)).toEqual({ result: "invalid_config_acknowledgement" });
    } finally {
      await stopChildProcess(plugin.process);
    }
  });

  it("does not expose gRPC status details returned by a plugin", async () => {
    const plugin = await startPlugin("rpc-error");
    try {
      const result = await runApply(plugin.endpoint);
      expect(result.exitCode).toBe(0);
      expect(JSON.parse(result.stdout)).toEqual({ result: "unavailable" });
      expect(`${result.stdout}${result.stderr}`).not.toContain("private plugin status detail");
    } finally {
      await stopChildProcess(plugin.process);
    }
  });
});
