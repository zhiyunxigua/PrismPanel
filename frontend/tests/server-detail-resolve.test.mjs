import assert from "node:assert/strict";
import test from "node:test";
import {
  SERVER_LIST_RETRIES,
  SERVER_LIST_RETRY_DELAY_MS,
  resolveServerList,
} from "../src/views/server-detail-resolve.js";

test("resolveServerList finds the target server and filters its instances", () => {
  const data = {
    servers: [
      { server_id: "a", name: "A" },
      { server_id: "b", name: "B" },
    ],
    instances: [
      { instance_id: "a_1", server_id: "a", state: "running" },
      { instance_id: "b_1", server_id: "b", state: "stopped" },
    ],
  };
  const outcome = resolveServerList(data, "b");
  assert.equal(outcome.status, "ok");
  assert.equal(outcome.server.name, "B");
  assert.deepEqual(outcome.instances.map((item) => item.instance_id), ["b_1"]);
});

test("resolveServerList reports node_error when the payload carries an error field", () => {
  const outcome = resolveServerList({ servers: [], error: { code: "DAEMON_TIMEOUT", message: "节点超时" } }, "a");
  assert.equal(outcome.status, "node_error");
  assert.equal(outcome.message, "节点超时");
});

test("resolveServerList reports node_error for a null payload", () => {
  const outcome = resolveServerList(null, "a");
  assert.equal(outcome.status, "node_error");
  assert.ok(outcome.message);
});

test("resolveServerList reports empty when servers array is absent or empty without error", () => {
  assert.equal(resolveServerList({}, "a").status, "empty");
  assert.equal(resolveServerList({ servers: [] }, "a").status, "empty");
  assert.equal(resolveServerList({ servers: null }, "a").status, "empty");
});

test("resolveServerList reports missing when the list is non-empty but lacks the target", () => {
  const outcome = resolveServerList({ servers: [{ server_id: "other", name: "Other" }] }, "a");
  assert.equal(outcome.status, "missing");
});

test("resolveServerList tolerates malformed server entries without crashing", () => {
  const outcome = resolveServerList({ servers: [null, { name: "NoId" }, { server_id: "a" }] }, "a");
  assert.equal(outcome.status, "ok");
  assert.equal(outcome.server.server_id, "a");
});

test("retry constants are sane", () => {
  assert.ok(Number.isInteger(SERVER_LIST_RETRIES) && SERVER_LIST_RETRIES >= 1);
  assert.ok(SERVER_LIST_RETRY_DELAY_MS >= 0);
});
