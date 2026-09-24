import { execFile, spawn, type ChildProcess, type ChildProcessWithoutNullStreams, type SpawnOptions } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

export interface GoFixtureBinary {
  executable: string;
  cleanup(): Promise<void>;
}

export async function buildGoFixture(repositoryRoot: string, packagePath: string): Promise<GoFixtureBinary> {
  const directory = await mkdtemp(join(tmpdir(), "liapoldus-plugin-fixture-"));
  const executable = join(directory, "fixture");
  try {
    await execFileAsync("go", ["build", "-o", executable, packagePath], { cwd: repositoryRoot });
  } catch (error) {
    await rm(directory, { recursive: true, force: true });
    throw error;
  }
  return {
    executable,
    cleanup: () => rm(directory, { recursive: true, force: true }),
  };
}

export function startGoFixture(executable: string, options: SpawnOptions = {}, args: string[] = []): ChildProcessWithoutNullStreams {
  return spawn(executable, args, { ...options, stdio: "pipe" });
}

export async function stopChildProcess(child: ChildProcess): Promise<void> {
  const waitForExit = new Promise<void>((resolve) => {
    if (child.exitCode !== null || child.signalCode !== null) {
      resolve();
      return;
    }
    child.once("exit", () => resolve());
    child.once("error", () => resolve());
  });

  if (child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGTERM");

  let timeout: NodeJS.Timeout | undefined;
  await Promise.race([
    waitForExit,
    new Promise<void>((resolve) => {
      timeout = setTimeout(resolve, 5_000);
    }),
  ]);
  if (timeout !== undefined) clearTimeout(timeout);

  if (child.exitCode === null && child.signalCode === null) {
    child.kill("SIGKILL");
    await waitForExit;
  }
}
