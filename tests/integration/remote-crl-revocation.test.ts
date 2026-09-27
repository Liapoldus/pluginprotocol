import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { beforeAll, afterAll, describe, expect, it } from "vitest";
import { buildGoFixture, type GoFixtureBinary } from "../support/child-process.js";

const execFileAsync = promisify(execFile);
const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
let fixture: GoFixtureBinary | undefined;

describe("remote mTLS CRL revocation", () => {
  beforeAll(async () => {
    fixture = await buildGoFixture(root, "./tests/fixtures/remote-crl-revocation");
  }, 30_000);

  afterAll(async () => {
    await fixture?.cleanup();
  });

  it("accepts a verified non-revoked plugin replica", async () => {
    const actual = await run("healthy");
    expect(actual).toEqual({ accepted: true });
  });

  it("rejects a revoked plugin server leaf and a revoked Gateway client leaf", async () => {
    expect(await run("revoked-plugin")).toEqual({ accepted: false });
    expect(await run("revoked-gateway")).toEqual({ accepted: false });
  });

  it("closes existing client channels on CRL update and never replays unknown calls", async () => {
    expect(await run("update-client")).toEqual({
      updateAccepted: true,
      activeChannelClosed: true,
      reconnected: false,
      replayed: false,
    });
  });

  it("closes existing server connections and denies revoked peers after CRL update", async () => {
    expect(await run("update-server")).toEqual({
      updateAccepted: true,
      activeChannelClosed: true,
      reconnected: false,
    });
  });

  it("fails closed on malformed or stale CRL bundles and closes active channels", async () => {
    expect(await run("invalid-update")).toEqual({
      updateAccepted: false,
      activeChannelClosed: true,
      reconnected: false,
    });
    expect(await run("stale-bundle")).toEqual({ accepted: false });
  });

  it("applies the same revocation policy to both remote GrantBroker peers", async () => {
    expect(await run("grant-broker")).toEqual({
      healthy: true,
      clientUpdateClosed: true,
      serverUpdateClosed: true,
    });
  });
});

async function run(scenario: string): Promise<Record<string, unknown>> {
  if (!fixture) throw new Error("remote CRL fixture was not built");
  const { stdout } = await execFileAsync(fixture.executable, [scenario], { cwd: root, timeout: 20_000 });
  return JSON.parse(stdout) as Record<string, unknown>;
}
