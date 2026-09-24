import { afterAll, beforeAll, describe, expect, it } from "vitest";
import type { ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { credentials } from "@grpc/grpc-js";
import { PluginServiceClient } from "../generated/liapoldus/plugin/v1/service.js";
import { InvocationMode } from "../generated/liapoldus/plugin/v1/control.js";
import { StreamCloseCode, StreamTransport, StreamDirection, WebSocketMessageKind } from "../generated/liapoldus/plugin/v1/service.js";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
let child: ChildProcessWithoutNullStreams | undefined;
let fixture: GoFixtureBinary | undefined;
let client: PluginServiceClient;

async function openFixture(): Promise<void> {
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
}

function collectStream() {
  const stream = client.stream();
  const messages: Array<Record<string, any>> = [];
  const finished = new Promise<void>((resolve, reject) => {
    stream.once("end", resolve);
    stream.once("error", reject);
  });
  stream.on("data", (message) => messages.push(message));
  return { stream, messages, finished };
}

describe("pluginprotocol v1 universal Stream over a real gRPC child process", () => {
  beforeAll(openFixture, 35_000);
  afterAll(async () => {
    client?.close();
    if (child) await stopChildProcess(child);
    await fixture?.cleanup();
  });

  it("returns Manifest capability modes for each declared capability", async () => {
    const manifest = await new Promise<any>((resolve, reject) => {
      client.manifest({}, (error, response) => error ? reject(error) : resolve(response));
    });
    const descriptor = manifest.capabilityDescriptors.find((entry: any) => entry.capability === "forms.live");
    expect(descriptor?.modes).toEqual([
      InvocationMode.INVOCATION_MODE_HTTP_STREAM,
      InvocationMode.INVOCATION_MODE_WEBSOCKET,
      InvocationMode.INVOCATION_MODE_SSE,
    ]);
  });

  it("streams HTTP upload chunks and a distinct response start/chunk sequence", async () => {
    const { stream, messages, finished } = collectStream();
    stream.write({ capability: "forms.live", open: {
      transport: StreamTransport.STREAM_TRANSPORT_UNSPECIFIED,
      mode: InvocationMode.INVOCATION_MODE_HTTP_STREAM,
      connectionId: "http-stream-1",
      contextJson: new TextEncoder().encode(JSON.stringify({ version: 1, kind: "http", method: "POST", path: "/upload", requestId: "request-1" })),
    } });
    stream.write({ capability: "forms.live", httpRequestChunk: { payload: new Uint8Array([0, 1]), endStream: false } });
    stream.write({ capability: "forms.live", httpRequestChunk: { payload: new Uint8Array([255, 128]), endStream: true } });
    stream.end();
    await finished;

    const bodyKeys = ["httpResponseStart", "httpResponseChunk", "websocketHandshake", "websocketMessage", "sseEvent", "close"];
    expect(messages.map((message) => bodyKeys.find((key) => message[key] !== undefined))).toEqual([
      "httpResponseStart", "httpResponseChunk", "httpResponseChunk", "close",
    ]);
    expect(messages[0].httpResponseStart.statusCode).toBe(200);
    expect(Array.from(messages[1].httpResponseChunk.payload)).toEqual([0, 1]);
    expect(Array.from(messages[2].httpResponseChunk.payload)).toEqual([255, 128]);
    expect(messages[2].httpResponseChunk.endStream).toBe(true);
    expect(messages[3].close.code).toBe(StreamCloseCode.STREAM_CLOSE_CODE_NORMAL);
  });

  it("negotiates only an offered WebSocket subprotocol and preserves message boundaries", async () => {
    const { stream, messages, finished } = collectStream();
    stream.write({ capability: "forms.live", open: {
      transport: StreamTransport.STREAM_TRANSPORT_UNSPECIFIED,
      mode: InvocationMode.INVOCATION_MODE_WEBSOCKET,
      connectionId: "websocket-1",
      contextJson: new TextEncoder().encode(JSON.stringify({ version: 1, kind: "websocket", method: "GET", path: "/live", requestId: "request-2", offeredSubprotocols: ["forms.v1"] })),
    } });
    stream.write({ capability: "forms.live", websocketMessage: {
      kind: WebSocketMessageKind.WEBSOCKET_MESSAGE_KIND_TEXT,
      direction: StreamDirection.STREAM_DIRECTION_REQUEST,
      payload: new TextEncoder().encode("one complete message"),
    } });
    stream.write({ capability: "forms.live", close: { code: StreamCloseCode.STREAM_CLOSE_CODE_NORMAL } });
    stream.end();
    await finished;

    expect(messages[0].websocketHandshake).toMatchObject({ accepted: true, subprotocol: "forms.v1" });
    expect(messages[1].websocketMessage).toMatchObject({
      kind: WebSocketMessageKind.WEBSOCKET_MESSAGE_KIND_TEXT,
      direction: StreamDirection.STREAM_DIRECTION_RESPONSE,
    });
    expect(new TextDecoder().decode(messages[1].websocketMessage.payload)).toBe("one complete message");
  });

  it("rejects a WebSocket subprotocol that the client did not offer", async () => {
    const { stream, messages, finished } = collectStream();
    stream.write({ capability: "forms.live", open: {
      transport: StreamTransport.STREAM_TRANSPORT_UNSPECIFIED,
      mode: InvocationMode.INVOCATION_MODE_WEBSOCKET,
      connectionId: "websocket-2",
      contextJson: new TextEncoder().encode(JSON.stringify({ version: 1, kind: "websocket", method: "GET", path: "/live", requestId: "request-4", offeredSubprotocols: ["other.v1"] })),
    } });
    stream.end();
    await finished;

    expect(messages[0].websocketHandshake).toMatchObject({ accepted: false, subprotocol: "" });
  });

  it("rejects a data frame before the stream open lifecycle", async () => {
    const stream = client.stream();
    const failed = new Promise<string>((resolve) => stream.once("error", (error) => resolve(error.message)));
    stream.write({ capability: "peer.session", data: {
      payload: new Uint8Array([1]),
      direction: StreamDirection.STREAM_DIRECTION_REQUEST,
    } });
    stream.end();
    await expect(failed).resolves.toContain("INVALID_ARGUMENT");
  });

  it("returns structured SSE event fields without serializing them in the plugin", async () => {
    const { stream, messages, finished } = collectStream();
    stream.write({ capability: "forms.live", open: {
      transport: StreamTransport.STREAM_TRANSPORT_UNSPECIFIED,
      mode: InvocationMode.INVOCATION_MODE_SSE,
      connectionId: "sse-1",
      contextJson: new TextEncoder().encode(JSON.stringify({ version: 1, kind: "sse", method: "GET", path: "/events", requestId: "request-3" })),
    } });
    stream.end();
    await finished;

    expect(messages[0].sseEvent).toEqual({ data: "ready", event: "forms.ready", id: "event-1", retryMillis: 1500 });
    expect(messages[1].close.code).toBe(StreamCloseCode.STREAM_CLOSE_CODE_NORMAL);
  });
});
