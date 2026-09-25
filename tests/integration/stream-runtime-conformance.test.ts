import { afterAll, beforeAll, describe, expect, it } from "vitest";
import type { ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { credentials, status } from "@grpc/grpc-js";
import { PluginServiceClient, StreamCloseCode, StreamDirection, StreamTransport } from "../generated/liapoldus/plugin/v1/service.js";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
let child: ChildProcessWithoutNullStreams | undefined;
let fixture: GoFixtureBinary | undefined;
let client: PluginServiceClient;

describe("pluginprotocol v1 Stream runtime conformance", () => {
  beforeAll(async () => {
    fixture = await buildGoFixture(root, "./tests/fixtures/grpc-plugin");
    child = startGoFixture(fixture.executable, { cwd: root });
    const lines = createInterface({ input: child.stdout });
    const address = await new Promise<string>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("plugin fixture did not become ready")), 30_000);
      lines.once("line", (line) => { clearTimeout(timeout); resolve(line); });
      child?.once("error", reject);
      child?.stderr.on("data", (data) => reject(new Error(String(data))));
      child?.once("exit", (code) => reject(new Error(`plugin fixture exited (${code})`)));
    });
    client = new PluginServiceClient(address, credentials.createInsecure());
  }, 35_000);

  afterAll(async () => {
    client?.close();
    if (child) await stopChildProcess(child);
    await fixture?.cleanup();
  });

  it("enforces a caller deadline on an idle long-lived Stream", async () => {
    const stream = client.stream({ deadline: new Date(Date.now() + 250) });
    const terminal = terminalCode(stream);
    openTCP(stream, "idle-deadline");
    expect(await terminal).toBe(status.DEADLINE_EXCEEDED);
  });

  it("propagates cancellation of an open long-lived Stream", async () => {
    const stream = client.stream();
    const terminal = terminalCode(stream);
    openTCP(stream, "cancel-open");
    stream.cancel();
    expect(await terminal).toBe(status.CANCELLED);
  });

  it("signals bounded writable buffering when responses are deliberately not consumed", async () => {
    const stream = client.stream();
    stream.pause();
    const terminal = terminalCode(stream);
    openTCP(stream, "backpressure");
    const payload = new Uint8Array(512 * 1024);
    let observedBackpressure = false;
    let requestFrames = 0;
    for (let index = 0; index < 64; index += 1) {
      requestFrames += 1;
      if (!stream.write({ capability: "peer.session", data: { payload, direction: StreamDirection.STREAM_DIRECTION_REQUEST } })) {
        observedBackpressure = true;
        break;
      }
    }
    let responseFrames = 0;
    stream.on("data", (message) => {
      if (message.data?.direction === StreamDirection.STREAM_DIRECTION_RESPONSE) responseFrames += 1;
    });
    stream.resume();
    stream.write({ capability: "peer.session", close: { code: StreamCloseCode.STREAM_CLOSE_CODE_NORMAL } });
    stream.end();
    expect(await terminal).toBe(status.OK);
    expect(observedBackpressure).toBe(true);
    expect(responseFrames).toBe(requestFrames);
  });

  it("keeps concurrent close/cancel races isolated and terminal", async () => {
    const runs = Array.from({ length: 12 }, async (_, index) => {
      const stream = client.stream();
      let responses = 0;
      stream.on("data", () => { responses += 1; });
      const terminal = terminalCode(stream);
      openTCP(stream, `race-${index}`);
      stream.write({ capability: "peer.session", data: {
        payload: new Uint8Array([index]),
        direction: StreamDirection.STREAM_DIRECTION_REQUEST,
      } });
      stream.write({ capability: "peer.session", close: { code: StreamCloseCode.STREAM_CLOSE_CODE_NORMAL } });
      stream.end();
      const code = await terminal;
      expect(code).toBe(status.OK);
      expect(responses).toBe(2);
    });
    await Promise.all(runs);
  });
});

function openTCP(stream: ReturnType<PluginServiceClient["stream"]>, connectionId: string): void {
  stream.write({ capability: "peer.session", open: {
    transport: StreamTransport.STREAM_TRANSPORT_TCP,
    connectionId,
    contextJson: new TextEncoder().encode(JSON.stringify({ kind: "tcp", source: "127.0.0.1:1001", destination: "127.0.0.1:2002" })),
  } });
}

function terminalCode(stream: ReturnType<PluginServiceClient["stream"]>): Promise<number> {
  return new Promise((resolve) => {
    stream.once("error", (error: { code?: number }) => resolve(error.code ?? -1));
    stream.once("end", () => resolve(status.OK));
  });
}
