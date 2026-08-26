import assert from "node:assert/strict";
import test from "node:test";

import { describeTarget, summarizeBatchResult } from "../src/batchResult.js";

test("all success produces success message", () => {
  const result = summarizeBatchResult({ total: 3, succeeded: 3, failed: 0 }, []);
  assert.deepEqual(result, { type: "success", message: "3 个目标全部成功" });
});

test("no failures with zero succeeded stays success", () => {
  const result = summarizeBatchResult({ total: 0, succeeded: 0, failed: 0 }, []);
  assert.deepEqual(result, { type: "success", message: "0 个目标全部成功" });
});

test("partial failure produces warning with target details and reasons", () => {
  const failures = [
    { node_id: "node-a", server_id: "srv-1", error: { code: "INSTANCE_BUSY", message: "实例正在执行其他操作" } },
    { node_id: "node-b", server_id: "srv-2", instance_id: "srv-2_1", error: { code: "FORBIDDEN", message: "无权执行此操作" } },
  ];
  const result = summarizeBatchResult({ total: 4, succeeded: 2, failed: 2 }, failures);
  assert.equal(result.type, "warning");
  assert.equal(
    result.message,
    "2 个目标成功，2 个目标失败：node-a / srv-1（实例正在执行其他操作）、node-b / srv-2 / srv-2_1（无权执行此操作）",
  );
});

test("all failed produces error message without success prefix", () => {
  const failures = [{ node_id: "node-a", server_id: "srv-1", error: { code: "DAEMON_UNAVAILABLE", message: "守护进程当前不可用" } }];
  const result = summarizeBatchResult({ total: 2, succeeded: 0, failed: 2 }, failures);
  assert.equal(result.type, "error");
  assert.equal(result.message, "2 个目标失败：node-a / srv-1（守护进程当前不可用）");
});

test("failure without reason falls back to unknown error", () => {
  const failures = [{ node_id: "node-a", server_id: "srv-1" }];
  const result = summarizeBatchResult({ total: 1, succeeded: 0, failed: 1 }, failures);
  assert.equal(result.type, "error");
  assert.equal(result.message, "1 个目标失败：node-a / srv-1（未知错误）");
});

test("more than 5 failures are truncated with 等", () => {
  const failures = Array.from({ length: 7 }, (_, i) => ({
    node_id: "node-a",
    server_id: "srv-" + i,
    error: { code: "X", message: "err" + i },
  }));
  const result = summarizeBatchResult({ total: 7, succeeded: 0, failed: 7 }, failures);
  assert.equal(result.type, "error");
  assert.equal(
    result.message,
    "7 个目标失败：node-a / srv-0（err0）、node-a / srv-1（err1）、node-a / srv-2（err2）、node-a / srv-3（err3）、node-a / srv-4（err4） 等",
  );
});

test("describeTarget joins node and instance selectors", () => {
  assert.equal(describeTarget({ node_id: "n1", server_id: "g", instance_id: "g_1" }), "n1 / g / g_1");
  assert.equal(describeTarget({ node_id: "n1", server_id: "g" }), "n1 / g");
  assert.equal(describeTarget({ node_id: "n1", instance_id: "g_1" }), "n1 / g_1");
});
