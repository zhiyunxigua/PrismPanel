// 批量服务器操作结果提示汇总（纯函数，便于单元测试）：
// - 无失败 → success（绿色）
// - 部分失败 → warning，并列出失败目标与原因（前 5 条 + 「等 N 项」截断）
// - 全部失败 → error
// 提示风格与 frontend/src/uploadResult.js（t5 上传/操作提示直观化）保持一致。

export function summarizeBatchResult(summary, failures, describe = describeTarget) {
  if (!summary || summary.failed === 0) {
    return { type: "success", message: (summary?.succeeded || 0) + " 个目标全部成功" };
  }
  const shown = failures.slice(0, 5)
    .map((item) => describe(item) + (item.error?.message ? "（" + item.error.message + "）" : "（未知错误）"))
    .join("、");
  const more = failures.length > 5 ? " 等" : "";
  const failedText = summary.failed + " 个目标失败" + (shown ? "：" + shown + more : "");
  if (summary.succeeded === 0) return { type: "error", message: failedText };
  return { type: "warning", message: summary.succeeded + " 个目标成功，" + failedText };
}

// 默认目标描述：node_id / server_id / instance_id 逐段拼接。
export function describeTarget(target) {
  const parts = [target.node_id];
  if (target.server_id) parts.push(target.server_id);
  if (target.instance_id) parts.push(target.instance_id);
  return parts.join(" / ");
}
