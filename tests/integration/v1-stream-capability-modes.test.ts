import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const source = async (path: string) => readFile(join(root, path), "utf8");

describe("pluginprotocol v1 invocation modes and universal Stream", () => {
  it("advertises supported invocation modes per Manifest capability", async () => {
    const proto = await source("proto/liapoldus/plugin/v1/control.proto");

    expect(proto).toMatch(/message\s+CapabilityDescriptor\s*\{/);
    expect(proto).toMatch(/repeated\s+InvocationMode\s+modes\s*=\s*2\s*;/);
    expect(proto).toMatch(/repeated\s+CapabilityDescriptor\s+capability_descriptors\s*=\s*4\s*;/);
    expect(proto).toMatch(/INVOCATION_MODE_CALL\s*=\s*1\s*;/);
    expect(proto).toMatch(/INVOCATION_MODE_HTTP_STREAM\s*=\s*2\s*;/);
    expect(proto).toMatch(/INVOCATION_MODE_WEBSOCKET\s*=\s*3\s*;/);
    expect(proto).toMatch(/INVOCATION_MODE_SSE\s*=\s*4\s*;/);
    expect(proto).toMatch(/INVOCATION_MODE_TCP\s*=\s*5\s*;/);
    expect(proto).toMatch(/INVOCATION_MODE_UDP\s*=\s*6\s*;/);
  });

  it("adds HTTP, WebSocket and SSE lifecycle messages without renumbering L4 fields", async () => {
    const proto = await source("proto/liapoldus/plugin/v1/service.proto");

    expect(proto).toMatch(/message\s+StreamOpen\s*\{[\s\S]*?StreamTransport\s+transport\s*=\s*1\s*;[\s\S]*?string\s+connection_id\s*=\s*2\s*;[\s\S]*?bytes\s+context_json\s*=\s*3\s*;/);
    expect(proto).toMatch(/optional\s+InvocationMode\s+mode\s*=\s*4\s*;/);
    expect(proto).toMatch(/bytes\s+payload\s*=\s*2\s*;/);
    expect(proto).toMatch(/StreamOpen\s+open\s*=\s*4\s*;/);
    expect(proto).toMatch(/StreamData\s+data\s*=\s*5\s*;/);
    expect(proto).toMatch(/StreamClose\s+close\s*=\s*6\s*;/);
    expect(proto).toMatch(/HttpRequestChunk\s+http_request_chunk\s*=\s*7\s*;/);
    expect(proto).toMatch(/HttpResponseStart\s+http_response_start\s*=\s*8\s*;/);
    expect(proto).toMatch(/HttpResponseChunk\s+http_response_chunk\s*=\s*9\s*;/);
    expect(proto).toMatch(/WebSocketHandshakeResult\s+websocket_handshake\s*=\s*10\s*;/);
    expect(proto).toMatch(/WebSocketMessage\s+websocket_message\s*=\s*11\s*;/);
    expect(proto).toMatch(/SseEvent\s+sse_event\s*=\s*12\s*;/);
  });

  it("defines distinct response-start and chunked-body data with WebSocket boundaries and structured SSE", async () => {
    const proto = await source("proto/liapoldus/plugin/v1/service.proto");

    expect(proto).toMatch(/message\s+HttpResponseStart\s*\{[\s\S]*?uint32\s+status_code\s*=\s*1\s*;[\s\S]*?bytes\s+metadata_json\s*=\s*2\s*;/);
    expect(proto).toMatch(/message\s+HttpResponseChunk\s*\{[\s\S]*?bytes\s+payload\s*=\s*1\s*;[\s\S]*?bool\s+end_stream\s*=\s*2\s*;/);
    expect(proto).toMatch(/message\s+WebSocketHandshakeResult\s*\{[\s\S]*?bool\s+accepted\s*=\s*1\s*;[\s\S]*?string\s+subprotocol\s*=\s*2\s*;/);
    expect(proto).toMatch(/message\s+WebSocketMessage\s*\{[\s\S]*?WebSocketMessageKind\s+kind\s*=\s*1\s*;[\s\S]*?bytes\s+payload\s*=\s*2\s*;/);
    expect(proto).toMatch(/message\s+SseEvent\s*\{[\s\S]*?string\s+data\s*=\s*1\s*;[\s\S]*?string\s+event\s*=\s*2\s*;[\s\S]*?string\s+id\s*=\s*3\s*;[\s\S]*?uint32\s+retry_millis\s*=\s*4\s*;/);
  });

  it("publishes versioned open-context and response-start metadata schemas", async () => {
    const open = JSON.parse(await source("contracts/protocol/v1/stream-open-context.schema.json"));
    const response = JSON.parse(await source("contracts/protocol/v1/http-stream-response-metadata.schema.json"));

    expect(open.oneOf.map((variant: any) => variant.properties?.kind?.const)).toEqual([
      "tcp", "udp", "http", "websocket", "sse",
    ]);
    expect(response.required).toContain("version");
    expect(response.properties.cookies.$ref).toBe("../../http/v1/response-action.schema.json#/properties/cookies");
  });
});
