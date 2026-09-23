import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("pluginprotocol v1 L4 stream lifecycle", () => {
  it("defines typed open/data/close variants for raw TCP and UDP bytes", async () => {
    const proto = await readFile(`${root}/proto/liapoldus/plugin/v1/service.proto`, "utf8");
    const openContextSchema = JSON.parse(await readFile(`${root}/contracts/protocol/v1/stream-open-context.schema.json`, "utf8"));

    expect(proto).toMatch(/enum StreamTransport\s*\{[^}]*STREAM_TRANSPORT_TCP[^}]*STREAM_TRANSPORT_UDP/s);
    expect(proto).toMatch(/enum StreamDirection\s*\{[^}]*STREAM_DIRECTION_REQUEST[^}]*STREAM_DIRECTION_RESPONSE/s);
    expect(proto).toMatch(/enum StreamCloseCode\s*\{[^}]*STREAM_CLOSE_CODE_NORMAL[^}]*STREAM_CLOSE_CODE_DROP[^}]*STREAM_CLOSE_CODE_ERROR/s);
    expect(proto).toMatch(/message StreamOpen\s*\{[^}]*StreamTransport transport\s*=\s*1;[^}]*string connection_id\s*=\s*2;[^}]*bytes context_json\s*=\s*3;/s);
    expect(proto).toMatch(/message StreamData\s*\{[^}]*bytes payload\s*=\s*1;[^}]*StreamDirection direction\s*=\s*2;/s);
    expect(proto).toMatch(/message StreamClose\s*\{[^}]*StreamCloseCode code\s*=\s*1;/s);
    expect(proto).toMatch(/oneof body\s*\{[^}]*StreamOpen open\s*=\s*4;[^}]*StreamData data\s*=\s*5;[^}]*StreamClose close\s*=\s*6;/s);
    expect(proto).toContain("bytes payload = 2;");
    expect(proto).toContain("Deprecated untyped payload.");
    expect(openContextSchema.oneOf).toHaveLength(2);
    expect(openContextSchema.oneOf.map((context: { properties: { kind: { const: string } } }) => context.properties.kind.const)).toEqual(["tcp", "udp"]);
    const propertyNames = openContextSchema.oneOf.flatMap((context: { properties: Record<string, unknown> }) => Object.keys(context.properties));
    expect(propertyNames).not.toEqual(expect.arrayContaining(["socket", "path", "secret", "credential"]));
  });
});
