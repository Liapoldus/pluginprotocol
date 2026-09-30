import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { buildGoFixture, startGoFixture, stopChildProcess, type GoFixtureBinary } from "../support/child-process.js";

const root = fileURLToPath(new URL("../..", import.meta.url));
const FIXTURE_PACKAGE = "./tests/fixtures/peer-router";

interface Limits {
  max_message_bytes: number;
  max_stream_message_bytes: number;
  max_concurrent_calls: number;
  max_concurrent_streams: number;
}

interface Response {
  id: string;
  ok: boolean;
  error?: string;
  payload?: string;
  methods?: string[];
  outbox?: string[];
  received?: number;
  overloaded?: number;
  succeeded?: number;
  other?: number;
  limits?: Limits;
}

class PeerRouterFixture {
  private nextId = 0;
  private pending = new Map<string, (value: Response) => void>();

  constructor(private readonly child: ChildProcessWithoutNullStreams) {
    const lines = createInterface({ input: child.stdout });
    lines.on("line", (line) => {
      const parsed = JSON.parse(line) as Response;
      // Go omits zero counters; normalise so assertions read naturally.
      const response: Response = {
        ...parsed,
        received: parsed.received ?? 0,
        succeeded: parsed.succeeded ?? 0,
        overloaded: parsed.overloaded ?? 0,
        other: parsed.other ?? 0,
      };
      const resolve = this.pending.get(response.id);
      if (resolve) {
        this.pending.delete(response.id);
        resolve(response);
      }
    });
  }

  send(request: Record<string, unknown>): Promise<Response> {
    this.nextId += 1;
    const id = `r${this.nextId}`;
    return new Promise<Response>((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`peer-router fixture did not answer ${id}`));
      }, 30_000);
      this.pending.set(id, (value) => {
        clearTimeout(timeout);
        resolve(value);
      });
      this.child.stdin.write(`${JSON.stringify({ id, ...request })}\n`);
    });
  }
}

let fixture: GoFixtureBinary | undefined;
let child: ChildProcessWithoutNullStreams | undefined;
let router: PeerRouterFixture;
let limits: Limits;

const encode = (value: string) => Buffer.from(value, "utf8").toString("base64");
const decode = (value: string) => Buffer.from(value, "base64").toString("utf8");

describe("generic peer registration and dispatch", () => {
  beforeAll(async () => {
    fixture = await buildGoFixture(root, FIXTURE_PACKAGE);
    child = startGoFixture(fixture.executable, { cwd: root });
    router = new PeerRouterFixture(child);
    limits = (await router.send({ op: "limits" })).limits!;
  }, 60_000);

  afterAll(async () => {
    if (child) await stopChildProcess(child);
    if (fixture) await fixture.cleanup();
  });

  it("hands a unary handler the identity of the peer it authenticated", async () => {
    // A stream handler can ask the stream who opened it, but a unary handler had no
    // way to learn the same thing, which forced a consumer to duplicate its identity
    // logic outside the handler. The identity is the one the transport authenticated,
    // never one the caller supplied in the request.
    const response = await router.send({ op: "call", method: "example.whoami", caller_identity: "urn:test:authenticated-caller" });
    expect(response.ok).toBe(true);
    expect(decode(response.payload!)).toBe("urn:test:authenticated-caller");
  });

  it("never lets a caller dictate the identity its handler is told", async () => {
    const response = await router.send({ op: "call", method: "example.whoami" });
    expect(response.ok).toBe(true);
    expect(decode(response.payload!)).toBe("urn:test:caller");
  });

  it("exposes a fully resolved bounded budget", () => {
    expect(limits.max_message_bytes).toBeGreaterThan(0);
    expect(limits.max_stream_message_bytes).toBeGreaterThan(0);
    expect(limits.max_concurrent_calls).toBeGreaterThan(0);
    expect(limits.max_concurrent_streams).toBeGreaterThan(0);
  });

  it("serves an arbitrary consumer-defined method name", async () => {
    const response = await router.send({ op: "call", method: "example.echo", payload: encode("hello") });
    expect(response.ok).toBe(true);
    expect(decode(response.payload!)).toBe("hello");
  });

  it("never interprets the payload of an arbitrary method", async () => {
    const response = await router.send({ op: "call", method: "example.upper", payload: encode("hello") });
    expect(response.ok).toBe(true);
    expect(decode(response.payload!)).toBe("HELLO");
  });

  it("reports every registered method in a stable order", async () => {
    const response = await router.send({ op: "methods" });
    expect(response.ok).toBe(true);
    expect(response.methods).toEqual([...response.methods!].sort());
    expect(response.methods).toContain("example.echo");
    expect(response.methods).toContain("example.stream.echo");
  });

  it("rejects an unregistered method instead of failing open", async () => {
    const response = await router.send({ op: "call", method: "does.not.exist", payload: "" });
    expect(response.ok).toBe(false);
    expect(response.error).toBe("peer: method not found");
  });

  it("rejects a second registration of the same method", async () => {
    const response = await router.send({ op: "register", method: "example.echo" });
    expect(response.ok).toBe(false);
    expect(response.error).toBe("peer: method already registered");
  });

  it("propagates an invocation deadline to the handler", async () => {
    const response = await router.send({
      op: "call",
      method: "example.slow",
      payload: encode(JSON.stringify({ hold_ms: 5_000 })),
      deadline_ms: 50,
    });
    expect(response.ok).toBe(false);
    expect(response.error).toBe("context deadline exceeded");
  });

  it("isolates a panicking handler as a sanitized internal failure", async () => {
    const response = await router.send({ op: "call", method: "example.panic", payload: "" });
    expect(response.ok).toBe(false);
    expect(response.error).toContain("peer: internal error");
    const afterwards = await router.send({ op: "call", method: "example.echo", payload: encode("still alive") });
    expect(afterwards.ok).toBe(true);
    expect(decode(afterwards.payload!)).toBe("still alive");
  });

  it("isolates a panicking stream handler as a sanitized internal failure", async () => {
    const response = await router.send({ op: "stream", method: "example.stream.panic", inbound: [] });
    expect(response.ok).toBe(false);
    expect(response.error).toContain("peer: internal error");
  });

  it("rejects a response larger than the negotiated limit", async () => {
    const response = await router.send({
      op: "call",
      method: "example.oversize",
      payload: encode(JSON.stringify({ size: limits.max_message_bytes + 1 })),
    });
    expect(response.ok).toBe(false);
    expect(response.error).toBe("peer: message exceeds size limit");
  });

  it("serves a bidirectional stream preserving frame order", async () => {
    const frames = ["alpha", "beta", "gamma"].map(encode);
    const response = await router.send({ op: "stream", method: "example.stream.echo", inbound: frames });
    expect(response.ok).toBe(true);
    expect(response.received).toBe(3);
    expect(response.outbox!.map(decode)).toEqual(["alpha", "beta", "gamma"]);
  });

  it("rejects a stream for an unregistered method", async () => {
    const response = await router.send({ op: "stream", method: "nope.stream", inbound: [] });
    expect(response.ok).toBe(false);
    expect(response.error).toBe("peer: method not found");
  });

  it("bounds outbound buffering instead of growing without limit", async () => {
    const response = await router.send({
      op: "stream",
      method: "example.stream.flood",
      queue_depth: 2,
      inbound: [encode(JSON.stringify({ outbound: 16 }))],
    });
    expect(response.ok).toBe(false);
    expect(response.error).toBe("peer: send queue is full");
    expect(response.outbox).toHaveLength(2);
  });

  it("rejects a stream whose deadline expires while it is open", async () => {
    const response = await router.send({
      op: "stream",
      method: "example.stream.block",
      deadline_ms: 50,
      inbound: [encode(JSON.stringify({ hold_ms: 5_000 }))],
    });
    expect(response.ok).toBe(false);
    expect(response.error).toBe("context deadline exceeded");
  });

  it("admits exactly the configured number of concurrent calls", async () => {
    const response = await router.send({ op: "flood", count: 200, deadline_ms: 250 });
    expect(response.ok).toBe(true);
    expect(response.succeeded).toBe(limits.max_concurrent_calls);
    expect(response.overloaded).toBe(200 - limits.max_concurrent_calls);
    expect(response.other).toBe(0);
  });

  it("admits exactly the configured number of concurrent streams", async () => {
    const response = await router.send({ op: "flood_streams", count: 80 });
    expect(response.ok).toBe(true);
    expect(response.succeeded).toBe(limits.max_concurrent_streams);
    expect(response.overloaded).toBe(80 - limits.max_concurrent_streams);
    expect(response.other).toBe(0);
  });

  it("enforces authorization above the carrier for calls and streams", async () => {
    const deny = await router.send({ op: "deny", deny: ["example.echo", "example.stream.echo"] });
    expect(deny.ok).toBe(true);

    const call = await router.send({ op: "call", method: "example.echo", payload: encode("hi") });
    expect(call.ok).toBe(false);
    expect(call.error).toBe("peer: method is not authorized");

    const stream = await router.send({ op: "stream", method: "example.stream.echo", inbound: [encode("hi")] });
    expect(stream.ok).toBe(false);
    expect(stream.error).toBe("peer: method is not authorized");

    const allowed = await router.send({ op: "call", method: "example.upper", payload: encode("hi") });
    expect(allowed.ok).toBe(true);

    const reset = await router.send({ op: "deny", deny: [] });
    expect(reset.ok).toBe(true);

    const restored = await router.send({ op: "call", method: "example.echo", payload: encode("hi") });
    expect(restored.ok).toBe(true);
  });

  it("accepts any consumer-defined method name", async () => {
    const method = `consumer.${Date.now()}.custom`;
    const registered = await router.send({ op: "register", method });
    expect(registered.ok).toBe(true);

    const stream = await router.send({ op: "register_stream", method });
    expect(stream.ok).toBe(true);

    const methods = await router.send({ op: "methods" });
    expect(methods.methods).toContain(method);
  });

  it("rejects a second stream registration of the same method", async () => {
    const method = `consumer.${Date.now()}.twice`;
    const first = await router.send({ op: "register_stream", method });
    expect(first.ok).toBe(true);

    const second = await router.send({ op: "register_stream", method });
    expect(second.ok).toBe(false);
    expect(second.error).toBe("peer: method already registered");
  });

  it("rejects an empty method name", async () => {
    const response = await router.send({ op: "call", method: "", payload: "" });
    expect(response.ok).toBe(false);
    expect(response.error).toBe("peer: method not found");
  });

  it("survives a malformed request without terminating", async () => {
    const response = await router.send({ op: "not-a-real-op" });
    expect(response.ok).toBe(false);
    expect(response.error).toContain("unknown op");

    const afterwards = await router.send({ op: "call", method: "example.echo", payload: encode("still alive") });
    expect(afterwards.ok).toBe(true);
    expect(decode(afterwards.payload!)).toBe("still alive");
  });
});
