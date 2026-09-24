import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { execFile, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { credentials, Metadata, status } from "@grpc/grpc-js";
import { PluginServiceClient } from "../generated/liapoldus/plugin/v1/service.js";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);
let child: ChildProcessWithoutNullStreams | undefined;
let fixture: GoFixtureBinary | undefined;
let client: PluginServiceClient;
let pluginAddress: string;

function unary<T>(invoke: (callback: (error: Error | null, value?: T) => void) => unknown): Promise<T> {
  return new Promise((resolve, reject) => invoke((error, value) => error ? reject(error) : resolve(value as T)));
}

describe("gRPC plugin child process", () => {
  beforeAll(async () => {
    fixture = await buildGoFixture(root, "./tests/fixtures/grpc-plugin");
    child = startGoFixture(fixture.executable, { cwd: root });
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
    client = new PluginServiceClient(address, credentials.createInsecure());
    pluginAddress = address;
  }, 35_000);

  afterAll(async () => {
    client?.close();
    if (child) await stopChildProcess(child);
    await fixture?.cleanup();
  });

  it("performs typed handshake, keeps capability payload JSON, and streams in both directions", async () => {
    const manifest = await unary((callback) => client.manifest({}, callback));
    expect(manifest.protocolVersion).toBe("liapoldus.plugin.v1");
    expect(manifest.name).toBe("fixture");

    const schema = await unary((callback) => client.configSchema({}, callback));
    expect(schema).toBeDefined();
    const applied = await unary((callback) => client.configApply({ config: new Uint8Array([123, 125]) }, callback));
    expect(applied.applied).toBe(true);

    const response = await unary((callback) => client.call({
      capability: "forms.submit",
      payload: new TextEncoder().encode('{"value":"hello"}'),
      grants: [],
    }, callback));
    expect(new TextDecoder().decode(response.payload)).toBe('{"accepted":true}');

    const stream = client.stream();
    const received = new Promise<string>((resolve, reject) => {
      stream.once("data", (message) => resolve(new TextDecoder().decode(message.payload ?? new Uint8Array())));
      stream.once("error", reject);
    });
    stream.write({ capability: "forms.live", payload: new TextEncoder().encode('{"watch":true}') });
    expect(await received).toBe('{"event":"ready"}');
    stream.end();
  });

  it("rejects a stream message larger than the protocol bound", async () => {
    const stream = client.stream();
    const status = new Promise<number>((resolve, reject) => {
      stream.once("error", (error: { code: number }) => resolve(error.code));
      stream.once("data", () => reject(new Error("oversized stream message was accepted")));
    });
    stream.write({ capability: "forms.live", payload: new Uint8Array(2 << 20) });
    expect(await status).toBe(8);
  });

  it("rejects oversized unary protobuf messages at the server boundary", async () => {
    const largeClient = new PluginServiceClient(pluginAddress, credentials.createInsecure(), {
      "grpc.max_send_message_length": 12 << 20,
    });
    const code = await new Promise<number>((resolve) => {
      largeClient.call(
        { capability: "forms.submit", payload: new Uint8Array(11 << 20), grants: [] },
        (error) => resolve(error?.code ?? 0),
      );
    });
    largeClient.close();
    expect(code).toBe(status.RESOURCE_EXHAUSTED);
  });

  it("propagates a unary deadline to the plugin process", async () => {
    const code = await new Promise<number>((resolve) => {
      client.call(
        { capability: "forms.slow", payload: new TextEncoder().encode("{}"), grants: [] },
        new Metadata(),
        { deadline: new Date(Date.now() + 500) },
        (error) => resolve(error?.code ?? 0),
      );
    });
    expect(code).toBe(status.DEADLINE_EXCEEDED);
  });

  it("propagates explicit unary cancellation to the plugin process", async () => {
    const before = await unary((callback) => client.call({
      capability: "forms.cancelled",
      payload: new TextEncoder().encode("{}"),
      grants: [],
    }, callback));
    const countBefore = JSON.parse(new TextDecoder().decode(before.payload)).count as number;
    const code = new Promise<number>((resolve) => {
      const call = client.call({
        capability: "forms.slow",
        payload: new TextEncoder().encode("{}"),
        grants: [],
      }, (error) => resolve(error?.code ?? 0));
      setTimeout(() => call.cancel(), 50);
    });
    expect(await code).toBe(status.CANCELLED);

    const deadline = Date.now() + 2_000;
    let countAfter = countBefore;
    while (Date.now() < deadline && countAfter === countBefore) {
      const response = await unary((callback) => client.call({
        capability: "forms.cancelled",
        payload: new TextEncoder().encode("{}"),
        grants: [],
      }, callback));
      countAfter = JSON.parse(new TextDecoder().decode(response.payload)).count as number;
      if (countAfter === countBefore) await new Promise((resolve) => setTimeout(resolve, 25));
    }
    expect(countAfter).toBe(countBefore + 1);
  });

  it("classifies RPC cancellation through the public Go client", async () => {
    const before = await unary((callback) => client.call({
      capability: "forms.cancelled",
      payload: new TextEncoder().encode("{}"),
      grants: [],
    }, callback));
    const countBefore = JSON.parse(new TextDecoder().decode(before.payload)).count as number;
    const result = await execFileAsync("go", ["run", "./tests/fixtures/grpc-client", pluginAddress, "cancel"], { cwd: root });
    expect(JSON.parse(result.stdout)).toEqual({ error: "canceled" });

    const deadline = Date.now() + 2_000;
    let countAfter = countBefore;
    while (Date.now() < deadline && countAfter === countBefore) {
      const response = await unary((callback) => client.call({
        capability: "forms.cancelled",
        payload: new TextEncoder().encode("{}"),
        grants: [],
      }, callback));
      countAfter = JSON.parse(new TextDecoder().decode(response.payload)).count as number;
      if (countAfter === countBefore) await new Promise((resolve) => setTimeout(resolve, 25));
    }
    expect(countAfter).toBe(countBefore + 1);
  });

  it("serves concurrent unary capability calls independently", async () => {
    const started = Date.now();
    const responses = await Promise.all(Array.from({ length: 4 }, () => unary((callback) => client.call(
      {
        capability: "forms.delay",
        payload: new TextEncoder().encode('{"delayMs":250}'),
        grants: [],
      },
      callback,
    ))));
    const elapsed = Date.now() - started;
    expect(responses.map((response) => response.code)).toEqual(["", "", "", ""]);
    expect(responses.map((response) => new TextDecoder().decode(response.payload))).toEqual([
      '{"done":true}',
      '{"done":true}',
      '{"done":true}',
      '{"done":true}',
    ]);
    expect(elapsed).toBeLessThan(750);
  });
});
