import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { credentials } from "@grpc/grpc-js";
import { PluginServiceClient } from "../generated/liapoldus/plugin/v1/service.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
let child: ChildProcessWithoutNullStreams;
let client: PluginServiceClient;

function unary<T>(invoke: (callback: (error: Error | null, value?: T) => void) => unknown): Promise<T> {
  return new Promise((resolve, reject) => invoke((error, value) => error ? reject(error) : resolve(value as T)));
}

describe("gRPC plugin child process", () => {
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
});
