import assert from "node:assert/strict";
import test from "node:test";

import { buildUploadResultMessage } from "../src/uploadResult.js";

test("all success produces success message with counts", () => {
  const result = buildUploadResultMessage({
    parts: ["已上传 3 个文件", "已创建 1 个目录"],
    failed: 0,
  });
  assert.deepEqual(result, { type: "success", message: "已上传 3 个文件，已创建 1 个目录" });
});

test("no parts falls back to successEmpty text", () => {
  const result = buildUploadResultMessage({ parts: [], failed: 0, successEmpty: "未上传文件" });
  assert.deepEqual(result, { type: "success", message: "未上传文件" });
});

test("partial failure produces warning with file names and reasons", () => {
  const result = buildUploadResultMessage({
    parts: ["已上传 3 个文件"],
    failed: 2,
    failures: [
      { name: "a.jar", error: "权限不足" },
      { name: "b.jar", error: "网络错误" },
    ],
    succeeded: 3,
    noun: "文件",
  });
  assert.deepEqual(result, {
    type: "warning",
    message: "已上传 3 个文件，2 个文件上传失败：a.jar（权限不足）、b.jar（网络错误）",
  });
});

test("all failed produces error message without success prefix", () => {
  const result = buildUploadResultMessage({
    parts: [],
    failed: 5,
    failures: [{ name: "a.jar", error: "超时" }],
    succeeded: 0,
    noun: "插件",
  });
  assert.equal(result.type, "error");
  assert.equal(result.message, "5 个插件上传失败：a.jar（超时）");
});

test("more than 5 failures are truncated with 等", () => {
  const failures = Array.from({ length: 7 }, (_, i) => ({ name: "f" + i + ".jar" }));
  const result = buildUploadResultMessage({
    parts: [],
    failed: 7,
    failures,
    succeeded: 0,
    noun: "文件",
  });
  assert.equal(result.type, "error");
  assert.equal(result.message, "7 个文件上传失败：f0.jar、f1.jar、f2.jar、f3.jar、f4.jar 等");
});

test("failures without reason are listed by name only", () => {
  const result = buildUploadResultMessage({
    parts: ["已上传 1 个文件"],
    failed: 1,
    failures: [{ name: "c.yml" }],
    succeeded: 1,
    noun: "文件",
  });
  assert.equal(result.type, "warning");
  assert.equal(result.message, "已上传 1 个文件，1 个文件上传失败：c.yml");
});
