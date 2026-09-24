import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("v1 SSE retry presence", () => {
  it("preserves the distinction between an omitted retry and retry=0", () => {
    const proto = readFileSync(`${root}/proto/liapoldus/plugin/v1/service.proto`, "utf8");
    const generatedGo = readFileSync(`${root}/pluginv1/service.pb.go`, "utf8");
    const generatedTypeScript = readFileSync(`${root}/tests/generated/liapoldus/plugin/v1/service.ts`, "utf8");

    expect(proto).toMatch(/optional\s+uint32\s+retry_millis\s*=\s*4\s*;/);
    expect(generatedGo).toMatch(/RetryMillis\s+\*uint32\s+`protobuf:[^`]*oneof"/);
    expect(generatedTypeScript).toMatch(/export interface SseEvent\s*\{[^}]*retryMillis\?: number/s);
  });
});
