import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);

describe("pluginprotocol stream open context encoder", () => {
  it("encodes TCP and UDP metadata using the protocol-owned JSON schema", async () => {
    const result = await execFileAsync("go", ["run", "./tests/fixtures/stream-context"], { cwd: root });
    const contexts = result.stdout.trim().split("\n").map((line) => JSON.parse(line));

    expect(contexts).toEqual([
      { kind: "tcp", source: "127.0.0.1:1001", destination: "127.0.0.1:2002", sni: "forms.example", alpn: "h2" },
      { kind: "udp", source: "127.0.0.1:1001", destination: "127.0.0.1:2002" },
    ]);
  });
});
