// 上传/操作结果提示汇总（纯函数，便于单元测试）：
// - 无失败 → success（绿色）
// - 部分失败 → warning，并在消息中列出失败项名称与原因
// - 全部失败 → error
// 供 FileManager.vue / ServerDetailView.vue 等组件复用。

export function buildUploadResultMessage({
  parts = [],
  successEmpty = "未上传",
  failed = 0,
  failures = [],
  succeeded = 0,
  noun = "文件",
}) {
  const successText = parts.length ? parts.join("，") : successEmpty;
  if (!failed) return { type: "success", message: successText };
  const shown = failures.slice(0, 5)
    .map((item) => item.name + (item.error ? "（" + item.error + "）" : ""))
    .join("、");
  const more = failures.length > 5 ? " 等" : "";
  const failedText = failed + " 个" + noun + "上传失败" + (shown ? "：" + shown + more : "");
  if (succeeded === 0) return { type: "error", message: failedText };
  return { type: "warning", message: successText + "，" + failedText };
}

// showUploadResult 直接按结果类型弹出 ElMessage（elMessage 需是 ElMessage 本身或同签名对象）。
export function showUploadResult(elMessage, options) {
  const { type, message } = buildUploadResultMessage(options);
  elMessage[type](message);
}
