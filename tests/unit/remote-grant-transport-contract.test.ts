import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("remote GrantBroker transport contract", () => {
  it("requires authenticated TLS identities on both ends without changing loopback APIs", async () => {
    const source = await readFile(`${root}/transport/grants.go`, "utf8");

    expect(source).toMatch(/type RemoteGrantServerOptions struct/);
    expect(source).toMatch(/func NewRemoteGrantBrokerServer\(service pluginv1\.GrantBrokerServer, options RemoteGrantServerOptions\)/);
    expect(source).toMatch(/ExpectedClientIdentity string/);
    expect(source).toMatch(/func DialRemoteGrantBrokerContext\(ctx context\.Context, endpoint string, options RemoteTLSOptions\)/);
    expect(source).toMatch(/func RemoteGrantClientIdentity\(ctx context\.Context\) \(string, bool\)/);
    expect(source).toMatch(/ClientAuth:\s+tls\.RequireAndVerifyClientCert/);
    expect(source).toContain("credentials.NewTLS");
    expect(source).toMatch(/func NewGrantBrokerServer\(service pluginv1\.GrantBrokerServer\) \*GrantServer/);
    expect(source).toMatch(/func DialGrantBrokerContext\(ctx context\.Context, endpoint string\) \(\*GrantClient, error\)/);
  });

  it("keeps remote broker scope and channel identities in the canonical deployment contract", async () => {
    const contract = JSON.parse(await readFile(`${root}/contracts/protocol/v1/remote-deployment.json`, "utf8")) as {
      grantBroker?: Record<string, unknown>;
    };
    expect(contract.grantBroker).toMatchObject({
      remoteTransport: "TLS with mutual authentication",
      clientIdentity: "same unique remote plugin replica URI SAN used for its gRPC connection",
      serverIdentity: "configured Gateway deployment URI SAN, validated with the external trust bundle",
      publiclyExposed: false,
    });
  });
});
