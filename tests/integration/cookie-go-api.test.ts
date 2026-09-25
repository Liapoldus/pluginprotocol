import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

function runFixture(scenario: string): { error?: string; headers?: string[]; pairs?: string[]; redacted?: boolean } {
  const result = spawnSync("go", ["run", "./tests/fixtures/cookie-api", scenario], {
    cwd: root,
    encoding: "utf8",
  });
  expect(result.status, result.stderr).toBe(0);
  return JSON.parse(result.stdout);
}

describe("Go cookie contract API", () => {
  it("strictly parses header lines while preserving allowed pair order and multiplicity", () => {
    const result = runFixture("parse-cookie-header");
    expect(result.error).toBeUndefined();
    expect(result.pairs).toEqual(["theme=dark", "session=first", "session=second"]);
  });

  it("rejects malformed inbound Cookie pairs without reflecting secret values", () => {
    const result = runFixture("parse-cookie-header-invalid");
    expect(result.error).toBeTruthy();
    expect(result.redacted).toBe(true);
  });

  it("decodes ordinary and HttpOnly actions as separate Set-Cookie values", () => {
    const result = runFixture("typed-actions");
    expect(result.error).toBeUndefined();
    expect(result.headers).toHaveLength(2);
    expect(result.headers?.[0]).toContain("theme=light");
    expect(result.headers?.[0]).not.toContain("HttpOnly");
    expect(result.headers?.[1]).toContain("session=secret-value");
    expect(result.headers?.[1]).toContain("HttpOnly");
  });

  it.each(["unknown-field", "same-site-none", "host-prefix", "secure-prefix", "domain-invalid", "public-suffix"])(
    "rejects unsafe response action %s without leaking or producing headers",
    (scenario) => {
      const result = runFixture(scenario);
      expect(result.error).toBeTruthy();
      expect(result.headers ?? []).toEqual([]);
      expect(result.redacted).toBe(true);
    },
  );

  it("accepts a parent domain of the request host, including a host port", () => {
    const result = runFixture("domain-valid");
    expect(result.error).toBeUndefined();
    expect(result.headers?.[0]).toContain("Domain=example.com");
  });

  it("rejects every action atomically when any cookie action is invalid", () => {
    const result = runFixture("atomic");
    expect(result.error).toBeTruthy();
    expect(result.headers ?? []).toEqual([]);
  });

  it("filters exact scoped policy names while preserving duplicate pairs and order", () => {
    const result = runFixture("policy-filter");
    expect(result.error).toBeUndefined();
    expect(result.pairs).toEqual(["session=first", "session=second"]);
  });

  it.each(["policy-scope", "policy-invalid", "request-invalid"])("rejects invalid policy/request: %s", (scenario) => {
    const result = runFixture(scenario);
    expect(result.error).toBeTruthy();
    if (result.redacted !== undefined) expect(result.redacted).toBe(true);
  });
});
