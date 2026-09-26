import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { buildGoFixture } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("inherited plugin listener SDK", () => {
  it("accepts a Gateway-owned loopback listener through fd 3 in a child process", async () => {
    const fixture = await buildGoFixture(root, "./tests/fixtures/inherited-listener");
    try {
      const result = await new Promise<{ code: number; stdout: string; stderr: string }>((resolve, reject) => {
        const child = spawn(fixture.executable, [], { cwd: root });
        let stdout = "";
        let stderr = "";
        child.stdout.on("data", (chunk) => { stdout += String(chunk); });
        child.stderr.on("data", (chunk) => { stderr += String(chunk); });
        child.once("error", reject);
        child.once("exit", (code) => resolve({ code: code ?? 1, stdout, stderr }));
      });

      expect(result.code).toBe(0);
      expect(result.stdout.trim()).toBe("inherited-listener-accepted");
      expect(result.stderr).toBe("");
    } finally {
      await fixture.cleanup();
    }
  }, 35_000);
});
