import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { credentials } from "@grpc/grpc-js";
import { InvocationMode } from "../generated/liapoldus/plugin/v1/control.js";
import { PluginServiceClient } from "../generated/liapoldus/plugin/v1/service.js";
import { StreamCloseCode, StreamDirection, StreamTransport } from "../generated/liapoldus/plugin/v1/service.js";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
let fixture: GoFixtureBinary | undefined;
let child: ReturnType<typeof startGoFixture> | undefined;
let client: PluginServiceClient;

function unary<T>(invoke: (callback: (error: Error | null, value?: T) => void) => unknown): Promise<T> {
  return new Promise((resolve, reject) => invoke((error, value) => error ? reject(error) : resolve(value as T)));
}

describe("typed plugin handler registry", () => {
  beforeAll(async () => {
    fixture = await buildGoFixture(root, "./tests/fixtures/typed-registry");
    child = startGoFixture(fixture.executable, { cwd: root });
    const lines = createInterface({ input: child.stdout });
    const address = await new Promise<string>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("registry fixture did not become ready")), 30_000);
      lines.once("line", (line) => { clearTimeout(timeout); resolve(line); });
      child?.once("error", reject);
      child?.stderr.on("data", (data) => reject(new Error(String(data))));
      child?.once("exit", (code) => reject(new Error(`registry fixture exited (${code})`)));
    });
    client = new PluginServiceClient(address, credentials.createInsecure());
  }, 35_000);

  afterAll(async () => {
    client?.close();
    if (child) await stopChildProcess(child);
    await fixture?.cleanup();
  });

  it("registers unary handlers and exposes their invocation modes in Manifest", async () => {
    const manifest = await unary((callback) => client.manifest({}, callback));
    const descriptor = manifest.capabilityDescriptors.find((item) => item.capability === "sdk.echo");
    expect(descriptor?.modes).toEqual([InvocationMode.INVOCATION_MODE_CALL]);

    const response = await unary((callback) => client.call({
      capability: "sdk.echo",
      payload: new TextEncoder().encode('{"value":"hello"}'),
      grants: [],
    }, callback));
    expect(new TextDecoder().decode(response.payload)).toBe('{"value":"hello"}');
  });

  it("routes a bidi stream by its registered capability and mode", async () => {
    const stream = client.stream();
    const messages: Array<{ data?: { payload: Uint8Array; direction: number }; close?: { code: number } }> = [];
    const finished = new Promise<void>((resolve, reject) => {
      stream.once("end", resolve);
      stream.once("error", reject);
    });
    stream.on("data", (message) => messages.push(message));
    stream.write({ capability: "sdk.tcp", open: {
      transport: StreamTransport.STREAM_TRANSPORT_TCP,
      mode: InvocationMode.INVOCATION_MODE_TCP,
      connectionId: "registry-stream-1",
      contextJson: new TextEncoder().encode(JSON.stringify({ kind: "tcp", source: "127.0.0.1:1001", destination: "127.0.0.1:2002" })),
    } });
    stream.write({ capability: "sdk.tcp", data: {
      payload: new Uint8Array([1, 2, 255]),
      direction: StreamDirection.STREAM_DIRECTION_REQUEST,
    } });
    stream.write({ capability: "sdk.tcp", close: { code: StreamCloseCode.STREAM_CLOSE_CODE_NORMAL } });
    stream.end();
    await finished;

    expect(messages).toHaveLength(2);
    expect(Array.from(messages[0].data?.payload ?? [])).toEqual([1, 2, 255]);
    expect(messages[0].data?.direction).toBe(StreamDirection.STREAM_DIRECTION_RESPONSE);
    expect(messages[1].close?.code).toBe(StreamCloseCode.STREAM_CLOSE_CODE_NORMAL);
  });

  it("rejects a duplicate registration instead of silently replacing a handler", async () => {
    const duplicateChild = startGoFixture(fixture!.executable, { cwd: root }, ["duplicate"]);
    const lines = createInterface({ input: duplicateChild.stdout });
    const result = await new Promise<string>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("duplicate registration check timed out")), 10_000);
      lines.once("line", (line) => { clearTimeout(timeout); resolve(line); });
      duplicateChild.once("error", reject);
      duplicateChild.stderr.on("data", (data) => reject(new Error(String(data))));
    });
    await stopChildProcess(duplicateChild);
    expect(result).toBe("duplicate-rejected");
  });
});
