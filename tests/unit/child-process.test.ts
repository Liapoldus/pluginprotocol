import { spawn } from "node:child_process";
import { describe, expect, it } from "vitest";
import { stopChildProcess } from "../support/child-process.js";

describe("fixture child-process lifecycle", () => {
  it("waits until a terminated direct child has exited", async () => {
    const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], {
      stdio: "ignore",
    });

    await stopChildProcess(child);

    expect(child.signalCode).toBe("SIGTERM");
  });

  it("does not send a termination signal to an already exited child", async () => {
    const child = spawn(process.execPath, ["-e", "process.exit(0)"], {
      stdio: "ignore",
    });
    await new Promise<void>((resolve) => child.once("exit", () => resolve()));

    await expect(stopChildProcess(child)).resolves.toBeUndefined();
    expect(child.exitCode).toBe(0);
  });
});
