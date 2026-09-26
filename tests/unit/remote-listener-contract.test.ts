import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

describe("remote plugin listener v1 contract", () => {
  it("uses one fixed container listener across standalone, Docker, and Kubernetes", async () => {
    const contract = JSON.parse(await readFile(`${root}/contracts/protocol/v1/remote-listener.json`, "utf8"));
    expect(contract).toMatchObject({
      protocolVersion: "liapoldus.plugin.v1",
      bindAddress: "0.0.0.0:50051",
      transport: "tcp",
      servicePortMayDiffer: true,
      deploymentTargets: {
        standalone: { containerPort: 50051 },
        docker: { containerPort: 50051 },
        kubernetes: { targetPort: 50051 },
      },
    });
    expect(contract.security).toMatchObject({
      minimumTLSVersion: 772,
      clientAuth: 4,
      requireVerifiedClientCertificate: true,
      trustBundle: "plugin-workload-identities-only",
      insecureFallback: false,
    });

    const deployment = JSON.parse(await readFile(`${root}/contracts/protocol/v1/remote-deployment.json`, "utf8"));
    expect(deployment.modes.remote.listener).toEqual({ contract: "remote-listener.json" });
  });

  it("provides a typed TLS listener SDK helper backed by the versioned contract", async () => {
    const source = await readFile(`${root}/transport/remote_listener.go`, "utf8");
    expect(source).toMatch(/func ListenRemoteTLS\(service pluginv1\.PluginServiceServer, options RemoteServerOptions\) \(\*RemoteListener, error\)/);
    expect(source).toContain("ContractFiles()");
    expect(source).toContain("net.Listen(contract.Transport, contract.BindAddress)");
    expect(source).toContain("contract.BindAddress");
    expect(source).toContain("contract.Security.MinimumTLSVersion");
    expect(source).toContain("contract.Security.ClientAuth");
    expect(source).toContain("func (listener *RemoteListener) Serve() error");
    expect(source).toContain("func (listener *RemoteListener) Stop()");
    expect(source).not.toContain("0.0.0.0:50051");
  });
});
