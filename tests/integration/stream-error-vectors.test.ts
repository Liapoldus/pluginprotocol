import { afterAll, beforeAll, describe, expect, it } from "vitest";
import type { ChildProcessWithoutNullStreams } from "node:child_process";
import { readFile } from "node:fs/promises";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { credentials, status as grpcStatus } from "@grpc/grpc-js";
import { InvocationMode } from "../generated/liapoldus/plugin/v1/control.js";
import { PluginServiceClient } from "../generated/liapoldus/plugin/v1/service.js";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
let child: ChildProcessWithoutNullStreams | undefined;
let fixture: GoFixtureBinary | undefined;
let client: PluginServiceClient;

type StreamErrorVector = {
  name: string;
  fixtureScenario: string;
  expectedStatus: "RESOURCE_EXHAUSTED";
};

describe("executable Stream error vectors", () => {
  beforeAll(async () => {
    fixture = await buildGoFixture(root, "./tests/fixtures/stream-validator");
    child = startGoFixture(fixture.executable, { cwd: root });
    const lines = createInterface({ input: child.stdout });
    const address = await new Promise<string>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("stream validator fixture did not become ready")), 30_000);
      lines.once("line", (line) => { clearTimeout(timeout); resolve(line); });
      child?.once("error", reject);
      child?.stderr?.on("data", (data) => reject(new Error(String(data))));
      child?.once("exit", (code) => reject(new Error(`stream validator fixture exited (${code})`)));
    });
    client = new PluginServiceClient(address, credentials.createInsecure());
  }, 35_000);

  afterAll(async () => {
    client?.close();
    if (child) await stopChildProcess(child);
    await fixture?.cleanup();
  });

  it("executes the normative oversized SSE event error vector", async () => {
    const vectors = JSON.parse(await readFile(`${root}/contracts/protocol/v1/stream-error-vectors.json`, "utf8")) as StreamErrorVector[];
    const lifecycle = JSON.parse(await readFile(`${root}/contracts/protocol/v1/stream-lifecycle.json`, "utf8"));
    expect(vectors.map(({ name }) => name)).toEqual(["sse-event-over-limit"]);

    for (const vector of vectors) {
      expect(vector.expectedStatus).toBe("RESOURCE_EXHAUSTED");
      expect(lifecycle.status.limitExceeded).toBe(vector.expectedStatus);
      const stream = client.stream();
      const terminal = new Promise<number>((resolve, reject) => {
        const timeout = setTimeout(() => reject(new Error("Stream error vector did not finish")), 5_000);
        stream.on("data", () => undefined);
        stream.once("error", (error: { code?: number }) => { clearTimeout(timeout); resolve(error.code ?? -1); });
        stream.once("end", () => { clearTimeout(timeout); resolve(grpcStatus.OK); });
      });
      stream.write({ capability: "forms.live", open: {
        mode: InvocationMode.INVOCATION_MODE_SSE,
        connectionId: vector.fixtureScenario,
        contextJson: new TextEncoder().encode(JSON.stringify({ version: 1, kind: "sse", method: "GET", path: "/events", requestId: "vector-request" })),
      } });
      stream.end();
      expect(await terminal, vector.name).toBe(grpcStatus.RESOURCE_EXHAUSTED);
    }
  });
});
