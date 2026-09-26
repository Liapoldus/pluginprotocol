import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { readFile } from "node:fs/promises";
import { createInterface, type Interface as ReadLineInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { credentials, type ServiceError } from "@grpc/grpc-js";
import { BootstrapRequest, ConfigApplyRequest, ConfigApplyResult } from "../generated/liapoldus/plugin/v1/control.js";
import { GrantBrokerClient, GrantScope, type RedeemGrantRequest, type RedeemGrantResponse } from "../generated/liapoldus/plugin/v1/grant.js";
import { CallRequest, PluginServiceClient } from "../generated/liapoldus/plugin/v1/service.js";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
const instanceId = "forms-instance";
const purpose = "database-dsn";
const secretReference = "database-dsn-ref";
const secret = new TextEncoder().encode("fixture-secret-must-not-be-printed");

type FixtureGrant = {
  handle: string;
  purpose: string;
  instanceId: string;
  settingsRevision: string;
  secretReference: string;
  secret: string;
};

let fixture: GoFixtureBinary | undefined;
let broker: ChildProcessWithoutNullStreams | undefined;
let brokerLines: ReadLineInterface | undefined;
let brokerEndpoint = "";
const pluginLines = new Map<ChildProcessWithoutNullStreams, ReadLineInterface>();
const pluginClients = new Set<PluginServiceClient>();
let grantClient: GrantBrokerClient | undefined;

function nextLine(lines: ReadLineInterface, child: ChildProcessWithoutNullStreams): Promise<string> {
  return new Promise((resolve, reject) => {
    const onLine = (line: string) => {
      cleanup();
      resolve(line);
    };
    const onExit = (code: number | null) => {
      cleanup();
      reject(new Error(`config revision fixture exited: ${code}`));
    };
    const cleanup = () => {
      lines.off("line", onLine);
      child.off("exit", onExit);
    };
    lines.once("line", onLine);
    child.once("exit", onExit);
  });
}

async function startPlugin(): Promise<{ child: ChildProcessWithoutNullStreams; client: PluginServiceClient }> {
  if (!fixture) throw new Error("fixture not initialized");
  const child = startGoFixture(fixture.executable, { cwd: root }, ["plugin"]);
  const lines = createInterface({ input: child.stdout });
  pluginLines.set(child, lines);
  const endpoint = await nextLine(lines, child);
  const client = new PluginServiceClient(endpoint, credentials.createInsecure());
  pluginClients.add(client);
  return { child, client };
}

async function setGatewayGrants(grants: FixtureGrant[]): Promise<void> {
  if (!broker || !brokerLines) throw new Error("grant broker is not running");
  broker.stdin.write(`${JSON.stringify({ grants })}\n`);
  expect(await nextLine(brokerLines, broker)).toBe("updated");
}

function activeGrant(revision: string, handle: string) {
  return {
    handle,
    purpose,
    domains: [],
    capability: "",
    scope: GrantScope.GRANT_SCOPE_CONFIG_APPLY,
    instanceId,
    settingsRevision: revision,
    secretReference,
  };
}

function fixtureGrant(revision: string, handle: string): FixtureGrant {
  return { handle, purpose, instanceId, settingsRevision: revision, secretReference, secret: Buffer.from(secret).toString("base64") };
}

function bootstrap(client: PluginServiceClient) {
  return new Promise<void>((resolve, reject) => {
    client.bootstrap(BootstrapRequest.fromPartial({ instanceId, grantBrokerEndpoint: brokerEndpoint }), (error, response) => {
      if (error) return reject(error);
      if (!response.accepted) return reject(new Error("plugin rejected bootstrap"));
      resolve();
    });
  });
}

function apply(client: PluginServiceClient, revision: string, generation: number, grantHandle: string, rejectActivation = false) {
  return new Promise<ConfigApplyResult>((resolve, reject) => {
    const config = new TextEncoder().encode(JSON.stringify({ generation, rejectActivation }));
    client.configApply(ConfigApplyRequest.fromPartial({ config, settingsRevision: revision, grants: [activeGrant(revision, grantHandle)] }), (error, response) => {
      if (error) return reject(error);
      resolve(response);
    });
  });
}

function readActiveSettings(client: PluginServiceClient) {
  return new Promise<{ settingsRevision: string; generation: number; hasSecret: boolean }>((resolve, reject) => {
    client.call(CallRequest.fromPartial({ capability: "test.active-settings", payload: new TextEncoder().encode("{}") }), (error, response) => {
      if (error) return reject(error);
      try {
        resolve(JSON.parse(new TextDecoder().decode(response.payload)));
      } catch (decodeError) {
        reject(decodeError);
      }
    });
  });
}

function redeem(handle: string, revision: string) {
  if (!grantClient) throw new Error("grant client is not initialized");
  const request: RedeemGrantRequest = {
    handle,
    purpose,
    domain: "",
    capability: "",
    scope: GrantScope.GRANT_SCOPE_CONFIG_APPLY,
    instanceId,
    settingsRevision: revision,
    secretReference,
  };
  return new Promise<RedeemGrantResponse>((resolve, reject) => {
    grantClient!.redeemGrant(request, (error: ServiceError | null, response: RedeemGrantResponse) => {
      if (error) return reject(error);
      resolve(response);
    });
  });
}

async function expectRedeemed(handle: string, revision: string): Promise<void> {
  const result = await redeem(handle, revision);
  expect(Buffer.from(result.secret).equals(Buffer.from(secret))).toBe(true);
}

describe("ConfigApply settings revision and secret-grant lifecycle", () => {
  beforeAll(async () => {
    fixture = await buildGoFixture(root, "./tests/fixtures/config-revision-lifecycle");
    broker = startGoFixture(fixture.executable, { cwd: root }, ["broker"]);
    brokerLines = createInterface({ input: broker.stdout });
    brokerEndpoint = await nextLine(brokerLines, broker);
    grantClient = new GrantBrokerClient(brokerEndpoint, credentials.createInsecure());
  }, 35_000);

  afterAll(async () => {
    grantClient?.close();
    for (const client of pluginClients) client.close();
    for (const [child, lines] of pluginLines) {
      lines.close();
      await stopChildProcess(child);
    }
    brokerLines?.close();
    if (broker) await stopChildProcess(broker);
    await fixture?.cleanup();
  });

  it("defines secret-grant validity by the active settings revision", async () => {
    const contract = JSON.parse(await readFile(`${root}/contracts/protocol/v1/config-apply.json`, "utf8"));

    expect(contract.grantScope.lifetime).toContain("while the bound settings revision is active");
    expect(contract.grantScope.lifetime).toContain("failed candidate does not revoke grants for the previous active revision");
    expect(contract.rotation.oldHandles).toContain("revoked when the new revision activation succeeds or the plugin instance stops");
  });

  it("atomically rotates settings and grants, preserves the old revision on failed activation, and reissues grants after restart", async () => {
    const revision1 = "settings-r1";
    const revision2 = "settings-r2";
    const firstHandle = "gateway-handle-r1";
    const rejectedCandidateHandle = "gateway-handle-r2-rejected";
    const secondHandle = "gateway-handle-r2";

    await setGatewayGrants([fixtureGrant(revision1, firstHandle)]);
    const firstProcess = await startPlugin();
    await bootstrap(firstProcess.client);
    await expect(apply(firstProcess.client, revision1, 1, firstHandle)).resolves.toMatchObject({ applied: true, settingsRevision: revision1 });
    await expect(readActiveSettings(firstProcess.client)).resolves.toEqual({ settingsRevision: revision1, generation: 1, hasSecret: true });
    await expectRedeemed(firstHandle, revision1);

    await setGatewayGrants([fixtureGrant(revision1, firstHandle), fixtureGrant(revision2, rejectedCandidateHandle)]);
    await expect(apply(firstProcess.client, revision2, 2, rejectedCandidateHandle, true)).resolves.toMatchObject({ applied: false, settingsRevision: revision2 });
    await expect(readActiveSettings(firstProcess.client)).resolves.toEqual({ settingsRevision: revision1, generation: 1, hasSecret: true });
    await setGatewayGrants([fixtureGrant(revision1, firstHandle)]);
    await expectRedeemed(firstHandle, revision1);
    await expect(redeem(rejectedCandidateHandle, revision2)).rejects.toMatchObject({ code: 7 });

    await setGatewayGrants([fixtureGrant(revision1, firstHandle), fixtureGrant(revision2, secondHandle)]);
    await expect(apply(firstProcess.client, revision2, 2, secondHandle)).resolves.toMatchObject({ applied: true, settingsRevision: revision2 });
    await expect(readActiveSettings(firstProcess.client)).resolves.toEqual({ settingsRevision: revision2, generation: 2, hasSecret: true });
    await setGatewayGrants([fixtureGrant(revision2, secondHandle)]);
    await expect(redeem(firstHandle, revision1)).rejects.toMatchObject({ code: 7 });
    await expectRedeemed(secondHandle, revision2);

    firstProcess.client.close();
    pluginClients.delete(firstProcess.client);
    pluginLines.get(firstProcess.child)?.close();
    pluginLines.delete(firstProcess.child);
    await stopChildProcess(firstProcess.child);

    const restartedHandle = "gateway-handle-r2-reissued";
    await setGatewayGrants([fixtureGrant(revision2, restartedHandle)]);
    const restartedProcess = await startPlugin();
    await bootstrap(restartedProcess.client);
    await expect(apply(restartedProcess.client, revision2, 2, restartedHandle)).resolves.toMatchObject({ applied: true, settingsRevision: revision2 });
    await expect(readActiveSettings(restartedProcess.client)).resolves.toEqual({ settingsRevision: revision2, generation: 2, hasSecret: true });
    await expect(redeem(secondHandle, revision2)).rejects.toMatchObject({ code: 7 });
    await expectRedeemed(restartedHandle, revision2);
  }, 60_000);
});
