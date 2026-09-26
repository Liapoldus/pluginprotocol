import { afterAll, beforeAll, describe, expect, it } from "vitest";
import type { ChildProcessWithoutNullStreams } from "node:child_process";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { createInterface } from "node:readline";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);
let pluginFixture: GoFixtureBinary | undefined;
let clientFixture: GoFixtureBinary | undefined;
let brokerFixture: GoFixtureBinary | undefined;
let plugin: ChildProcessWithoutNullStreams | undefined;
let broker: ChildProcessWithoutNullStreams | undefined;
let pluginEndpoint = "";
let brokerEndpoint = "";

type ErrorVector = {
  name: string;
  endpoint: "plugin" | "grant-broker";
  clientMode: string;
  expectedClientError: string;
};

function waitForEndpoint(child: ChildProcessWithoutNullStreams): Promise<string> {
  const lines = createInterface({ input: child.stdout });
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("protocol error fixture did not become ready")), 30_000);
    lines.once("line", (line) => {
      clearTimeout(timeout);
      resolve(line);
    });
    child.once("error", reject);
    child.stderr.once("data", (data) => reject(new Error(String(data))));
    child.once("exit", (code) => reject(new Error(`protocol error fixture exited (${code})`)));
  });
}

describe("executable protocol error mapping vectors", () => {
  beforeAll(async () => {
    [pluginFixture, clientFixture, brokerFixture] = await Promise.all([
      buildGoFixture(root, "./tests/fixtures/grpc-plugin"),
      buildGoFixture(root, "./tests/fixtures/grpc-client"),
      buildGoFixture(root, "./tests/fixtures/grant-broker"),
    ]);
    plugin = startGoFixture(pluginFixture.executable, { cwd: root });
    broker = startGoFixture(brokerFixture.executable, { cwd: root });
    [pluginEndpoint, brokerEndpoint] = await Promise.all([waitForEndpoint(plugin), waitForEndpoint(broker)]);
  }, 45_000);

  afterAll(async () => {
    if (plugin) await stopChildProcess(plugin);
    if (broker) await stopChildProcess(broker);
    await pluginFixture?.cleanup();
    await clientFixture?.cleanup();
    await brokerFixture?.cleanup();
  });

  it("matches SDK results to the versioned executable vectors", async () => {
    const vectors = JSON.parse(await readFile(`${root}/contracts/protocol/v1/error-mapping-vectors.json`, "utf8")) as ErrorVector[];

    for (const vector of vectors) {
      const endpoint = vector.endpoint === "plugin" ? pluginEndpoint : brokerEndpoint;
      const result = await execFileAsync(clientFixture!.executable, [endpoint, vector.clientMode], { cwd: root });
      expect(JSON.parse(result.stdout), vector.name).toEqual({ error: vector.expectedClientError });
    }
  }, 60_000);
});
