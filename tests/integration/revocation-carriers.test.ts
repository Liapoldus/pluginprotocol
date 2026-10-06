import { spawnSync } from "node:child_process";
import { platform } from "node:os";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));
const carriers = platform() === "win32" ? ["pipe", "tcp"] : ["tcp", "quic", "unix"];

describe.each(carriers)("signed CRL revocation over %s", (carrier) => {
  it("fences a live session and prevents a revoked peer from dispatching after reconnect", () => {
    const goFlags = (process.env.LIAPOLDUS_PEER_FIXTURE_GOFLAGS ?? "")
      .split(/\s+/)
      .filter(Boolean);
    const result = spawnSync(
      "go",
      ["run", ...goFlags, "./tests/fixtures/revocation-security", "--scenario=carrier", `--carrier=${carrier}`],
      { cwd: root, encoding: "utf8", timeout: 60_000 },
    );

    expect(result.status, result.stderr).toBe(0);
    expect(JSON.parse(result.stdout)).toEqual({
      carrier,
      initialCallSucceeded: true,
      priorSessionFenced: true,
      revokedPeerRejectedPreDispatch: true,
    });
  }, 60_000);
});
