import { execFile } from "node:child_process";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const root = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);
let binary: Promise<string> | undefined;

async function probeBinary(): Promise<string> {
  binary ??= (async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-pluginprotocol-"));
    const path = join(directory, "protocol-probe");
    await execFileAsync("go", ["build", "-o", path, "./cmd/protocol-probe"], { cwd: root });
    return path;
  })();
  return binary;
}

export async function runProbe(...args: string[]) {
  const executable = await probeBinary();
  try {
    const { stdout, stderr } = await execFileAsync(executable, args);
    return { exitCode: 0, stdout, stderr };
  } catch (error: any) {
    return { exitCode: error.code ?? 1, stdout: error.stdout ?? "", stderr: error.stderr ?? "" };
  }
}
