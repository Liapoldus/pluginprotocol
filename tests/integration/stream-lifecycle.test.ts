import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { credentials } from "@grpc/grpc-js";
import { PluginServiceClient, StreamCloseCode, StreamDirection, StreamTransport } from "../generated/liapoldus/plugin/v1/service.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
let child: ChildProcessWithoutNullStreams;
let client: PluginServiceClient;

describe("pluginprotocol v1 L4 stream lifecycle", () => {
  beforeAll(async () => {
    child = spawn("go", ["run", "./tests/fixtures/grpc-plugin"], { cwd: root });
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
  }, 35_000);

  afterAll(() => {
    client?.close();
    child?.kill();
  });

  it("carries TCP open, raw directional data, and close as typed messages", async () => {
    const stream = client.stream();
    const received: Array<{ data?: { payload: Uint8Array; direction: StreamDirection }; close?: { code: StreamCloseCode } }> = [];
    const finished = new Promise<void>((resolve, reject) => {
      stream.once("end", resolve);
      stream.once("error", reject);
    });
    stream.on("data", (message) => received.push(message));

    stream.write({
      capability: "peer.session",
      open: {
        transport: StreamTransport.STREAM_TRANSPORT_TCP,
        connectionId: "tcp-session-1",
        contextJson: new TextEncoder().encode(JSON.stringify({ kind: "tcp", source: "127.0.0.1:1001", destination: "127.0.0.1:2002" })),
      },
    });
    stream.write({ capability: "peer.session", data: { payload: new Uint8Array([0, 1, 255]), direction: StreamDirection.STREAM_DIRECTION_REQUEST } });
    stream.write({ capability: "peer.session", data: { payload: new Uint8Array([128, 0]), direction: StreamDirection.STREAM_DIRECTION_REQUEST } });
    stream.write({ capability: "peer.session", close: { code: StreamCloseCode.STREAM_CLOSE_CODE_NORMAL } });
    stream.end();
    await finished;
    expect(received.map((message) => message.data?.direction ?? message.close?.code)).toEqual([
      StreamDirection.STREAM_DIRECTION_RESPONSE,
      StreamDirection.STREAM_DIRECTION_RESPONSE,
      StreamCloseCode.STREAM_CLOSE_CODE_NORMAL,
    ]);
    expect(received.slice(0, 2).map((message) => Array.from(message.data?.payload ?? []))).toEqual([[0, 1, 255], [128, 0]]);
  });

  it("uses one typed stream lifecycle for one UDP datagram", async () => {
    const stream = client.stream();
    const messages: Array<{ data?: { payload: Uint8Array; direction: StreamDirection }; close?: { code: StreamCloseCode } }> = [];
    const finished = new Promise<void>((resolve, reject) => {
      stream.once("end", resolve);
      stream.once("error", reject);
    });
    stream.on("data", (message) => messages.push(message));
    stream.write({
      capability: "peer.session",
      open: {
        transport: StreamTransport.STREAM_TRANSPORT_UDP,
        connectionId: "udp-datagram-1",
        contextJson: new TextEncoder().encode(JSON.stringify({ kind: "udp", source: "127.0.0.1:1001", destination: "127.0.0.1:2002" })),
      },
    });
    stream.write({ capability: "peer.session", data: { payload: new Uint8Array([0, 171, 255]), direction: StreamDirection.STREAM_DIRECTION_REQUEST } });
    stream.write({ capability: "peer.session", close: { code: StreamCloseCode.STREAM_CLOSE_CODE_NORMAL } });
    stream.end();
    await finished;
    expect(messages.map((message) => message.data?.direction ?? message.close?.code)).toEqual([
      StreamDirection.STREAM_DIRECTION_RESPONSE,
      StreamCloseCode.STREAM_CLOSE_CODE_NORMAL,
    ]);
    expect(Array.from(messages[0]?.data?.payload ?? [])).toEqual([0, 171, 255]);
  });
});
