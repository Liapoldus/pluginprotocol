import { afterAll, beforeAll, describe, expect, it } from "vitest";
import type { ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { credentials, status as grpcStatus } from "@grpc/grpc-js";
import { InvocationMode } from "../generated/liapoldus/plugin/v1/control.js";
import { PluginServiceClient, StreamCloseCode, StreamDirection, StreamTransport } from "../generated/liapoldus/plugin/v1/service.js";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
let child: ChildProcessWithoutNullStreams | undefined;
let fixture: GoFixtureBinary | undefined;
let client: PluginServiceClient;
let limits: { limits: { contextBytes: number; sseDataBytes: number; sseEventBytes: number; sseIDBytes: number; sseRetryMillis: number } };

describe("v1 Stream server-side lifecycle validation", () => {
  beforeAll(async () => {
    limits = JSON.parse(await (await import("node:fs/promises")).readFile(`${root}/contracts/protocol/v1/stream-lifecycle.json`, "utf8"));
    fixture = await buildGoFixture(root, "./tests/fixtures/stream-validator");
    child = startGoFixture(fixture.executable, { cwd: root });
    const lines = createInterface({ input: child.stdout });
    const address = await new Promise<string>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("stream validator fixture did not become ready")), 30_000);
      lines.once("line", (line) => { clearTimeout(timeout); resolve(line); });
      child?.once("error", reject);
      child?.stderr.on("data", (data) => reject(new Error(String(data))));
      child?.once("exit", (code) => reject(new Error(`stream validator fixture exited (${code})`)));
    });
    client = new PluginServiceClient(address, credentials.createInsecure());
  }, 35_000);

  afterAll(async () => {
    client?.close();
    if (child) await stopChildProcess(child);
    await fixture?.cleanup();
  });

  it("publishes a machine-readable normative lifecycle contract", async () => {
    const contract = JSON.parse(await (await import("node:fs/promises")).readFile(`${root}/contracts/protocol/v1/stream-lifecycle.json`, "utf8"));
    expect(contract.rules).toMatchObject({
      open: expect.stringContaining("first client message"),
      http: expect.stringContaining("response-start"),
      websocket: expect.stringContaining("accepted=false"),
      sse: expect.stringContaining("CR or NUL"),
      l4: expect.stringContaining("each datagram uses a separate Stream"),
      close: expect.stringContaining("Repeated Close"),
    });
  });

  it.each([
    ["message before Open", (stream: ReturnType<PluginServiceClient["stream"]>) => { stream.write({ capability: "forms.live", httpRequestChunk: { payload: new Uint8Array([1]) } }); }],
    ["mode and context kind mismatch", (stream: ReturnType<PluginServiceClient["stream"]>) => { open(stream, InvocationMode.INVOCATION_MODE_HTTP_STREAM, { version: 1, kind: "sse", method: "GET", path: "/events", requestId: "request-1" }); }],
    ["request chunk after request end_stream", (stream: ReturnType<PluginServiceClient["stream"]>) => { open(stream, InvocationMode.INVOCATION_MODE_HTTP_STREAM, httpContext()); stream.write({ capability: "forms.live", httpRequestChunk: { endStream: true } }); stream.write({ capability: "forms.live", httpRequestChunk: { payload: new Uint8Array([1]) } }); }],
    ["repeated client Close", (stream: ReturnType<PluginServiceClient["stream"]>) => { open(stream, InvocationMode.INVOCATION_MODE_TCP, l4Context("tcp"), StreamTransport.STREAM_TRANSPORT_TCP); stream.write({ capability: "forms.live", close: { code: StreamCloseCode.STREAM_CLOSE_CODE_NORMAL } }); stream.write({ capability: "forms.live", close: { code: StreamCloseCode.STREAM_CLOSE_CODE_NORMAL } }); }],
  ])("rejects %s on inbound frames", async (_name, writeInvalid) => {
    const stream = client.stream();
    const terminal = waitForTerminal(stream);
    writeInvalid(stream);
    stream.end();
    expect(await terminal).toBe(grpcStatus.INVALID_ARGUMENT);
  });

  it.each([
    ["HTTP response chunk before response-start", "http-chunk-before-start", InvocationMode.INVOCATION_MODE_HTTP_STREAM, httpContext()],
    ["duplicate HTTP response-start", "http-double-start", InvocationMode.INVOCATION_MODE_HTTP_STREAM, httpContext()],
    ["HTTP response chunk after end_stream", "http-after-end", InvocationMode.INVOCATION_MODE_HTTP_STREAM, httpContext()],
    ["WebSocket rejection with subprotocol", "ws-rejected-subprotocol", InvocationMode.INVOCATION_MODE_WEBSOCKET, wsContext()],
    ["unoffered WebSocket subprotocol", "ws-unoffered-subprotocol", InvocationMode.INVOCATION_MODE_WEBSOCKET, wsContext()],
    ["oversized SSE data field", "sse-large-data", InvocationMode.INVOCATION_MODE_SSE, sseContext()],
    ["newline in SSE event field", "sse-line-in-event", InvocationMode.INVOCATION_MODE_SSE, sseContext()],
  ])("rejects %s on outbound frames", async (_name, scenario, mode, context) => {
    expect(await runScenario(scenario, mode, context)).toBe(grpcStatus.INVALID_ARGUMENT);
  });

  it("allows multiline SSE data and a single terminal Close", async () => {
    expect(await runScenario("sse-valid", InvocationMode.INVOCATION_MODE_SSE, sseContext())).toBe(grpcStatus.OK);
  });

  it("uses limits from the protocol contract", () => {
    expect(limits.limits.contextBytes).toBeGreaterThan(0);
    expect(limits.limits.sseDataBytes).toBeGreaterThan(0);
    expect(limits.limits.sseEventBytes).toBeGreaterThan(0);
    expect(limits.limits.sseIDBytes).toBeGreaterThan(0);
    expect(limits.limits.sseRetryMillis).toBeGreaterThan(0);
  });
});

function open(stream: ReturnType<PluginServiceClient["stream"]>, mode: InvocationMode, context: unknown, transport = StreamTransport.STREAM_TRANSPORT_UNSPECIFIED): void {
  stream.write({ capability: "forms.live", open: { mode, transport, connectionId: "stream-1", contextJson: new TextEncoder().encode(JSON.stringify(context)) } });
}

function httpContext() { return { version: 1, kind: "http", method: "POST", path: "/upload", requestId: "request-1" }; }
function wsContext() { return { version: 1, kind: "websocket", method: "GET", path: "/socket", requestId: "request-1", offeredSubprotocols: ["forms.v1"] }; }
function sseContext() { return { version: 1, kind: "sse", method: "GET", path: "/events", requestId: "request-1" }; }
function l4Context(kind: "tcp" | "udp") { return { kind, source: "127.0.0.1:1001", destination: "127.0.0.1:2002" }; }

async function runScenario(scenario: string, mode: InvocationMode, context: unknown): Promise<number> {
  const stream = client.stream();
  const terminal = waitForTerminal(stream);
  open(stream, mode, { ...context as object, scenario }, mode === InvocationMode.INVOCATION_MODE_TCP ? StreamTransport.STREAM_TRANSPORT_TCP : StreamTransport.STREAM_TRANSPORT_UNSPECIFIED);
  stream.end();
  return terminal;
}

function waitForTerminal(stream: ReturnType<PluginServiceClient["stream"]>): Promise<number> {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("Stream validation did not finish")), 5_000);
    stream.on("data", () => undefined);
    stream.once("error", (error: { code?: number }) => { clearTimeout(timeout); resolve(error.code ?? -1); });
    stream.once("end", () => { clearTimeout(timeout); resolve(grpcStatus.OK); });
  });
}
