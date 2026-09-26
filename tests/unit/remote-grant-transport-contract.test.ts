import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("remote GrantBroker transport contract", () => {
  it("requires authenticated TLS identities on both ends without changing loopback APIs", async () => {
    const source = await readFile(`${root}/transport/grants.go`, "utf8");

    expect(source).toMatch(/type RemoteGrantServerOptions struct/);
    expect(source).toMatch(/func NewRemoteGrantBrokerServer\(service pluginv1\.GrantBrokerServer, options RemoteGrantServerOptions\)/);
    expect(source).toMatch(/AllowsClientIdentity func\(string\) bool/);
    expect(source).toMatch(/func DialRemoteGrantBrokerContext\(ctx context\.Context, endpoint string, options RemoteGrantTLSOptions\)/);
    expect(source).toMatch(/func RemoteGrantClientIdentity\(ctx context\.Context\) \(string, bool\)/);
    expect(source).toMatch(/remoteListenerTLSConfig\(options\.TLSCertificate, options\.ClientRoots\)/);
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
      tlsMinimumVersion: "TLS 1.3",
      identityAuthorization: "exact pre-registered plugin replica URI SAN from the verified TLS peer certificate",
      identityRequestField: false,
      secretMaterial: "RedeemGrantResponse.secret only",
      publiclyExposed: false,
    });
  });

  it("does not discover the Gateway callback endpoint through process environment", async () => {
    const source = await readFile(`${root}/transport/grants.go`, "utf8");
    const control = await readFile(`${root}/proto/liapoldus/plugin/v1/control.proto`, "utf8");

    expect(source).not.toContain("DialGrantBrokerFromEnvironmentContext");
    expect(source).not.toContain("GrantBrokerEndpointEnvironment");
    expect(source).toMatch(/func DialGrantBrokerFromBootstrapContext\(ctx context\.Context, bootstrap \*pluginv1\.BootstrapRequest, remoteOptions \*RemoteGrantTLSOptions\)/);
    expect(source).toMatch(/if remoteOptions != nil \{\s*return DialRemoteGrantBrokerContext/);
    expect(control).toMatch(/message BootstrapRequest\s*\{[^}]*grant_broker_endpoint/s);
  });
});
