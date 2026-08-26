import { ApiError, request } from "./api.js";
import { apiURL, runtimeHeaders, runtimeConfig } from "./runtime.js";

const proxyNodes = new Set();
const mutatingScopes = new Set(["file.edit", "file.upload", "file.import", "file.create", "file.move", "file.copy", "file.archive", "file.delete"]);

export async function fileJSON(authorization, method, body, extraHeaders = {}) {
  return withAuthorization(authorization, async (grant) => {
    const response = await fetch(endpointURL(grant), {
      method,
      headers: grantHeaders(grant, extraHeaders),
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: credentialsFor(grant),
    });
    return decodeJSON(response);
  });
}

export async function uploadFile(authorization, file, overwrite = false) {
  return uploadFileWithProgress(authorization, file, overwrite);
}

// XHR 版上传：通过 xhr.upload.onprogress 提供字节级进度回调（{loaded, total}）。
// 返回的 Promise 附带 cancel()（或传入 AbortSignal），可中止当前上传（xhr.abort()）。
// 无 onProgress / 未取消时行为与旧 fetch 版一致（仅丢失进度事件）。
export function uploadFileWithProgress(authorization, file, overwrite = false, onProgress, signal) {
  const controller = signal ? null : new AbortController();
  const activeSignal = signal || (controller ? controller.signal : null);
  const promise = withAuthorization(
    { ...authorization, size: file.size, overwrite },
    (grant) => xhrUpload(endpointURL(grant), grantHeaders(grant, {
      "Content-Type": file.type || "application/octet-stream",
      "X-Prism-Overwrite": String(overwrite),
    }), file, onProgress, activeSignal),
  );
  if (controller) promise.cancel = () => controller.abort();
  return promise;
}

export async function importArchive(authorization, file) {
  return withAuthorization(
    { ...authorization, size: file.size },
    async (grant) => {
      const response = await fetch(endpointURL(grant), {
        method: "POST",
        headers: grantHeaders(grant, { "Content-Type": "application/zip" }),
        body: file,
        credentials: credentialsFor(grant),
      });
      return decodeJSON(response);
    },
  );
}

export async function downloadFile(authorization, suggestedName, onProgress) {
  let writable;
  if (window.showSaveFilePicker) {
    const handle = await window.showSaveFilePicker({ suggestedName });
    writable = await handle.createWritable();
  }
  try {
    const response = await withAuthorization(authorization, async (grant) => {
      const result = await fetch(endpointURL(grant), {
        method: "GET",
        headers: grantHeaders(grant),
        credentials: credentialsFor(grant),
      });
      if (!result.ok) await decodeJSON(result);
      return result;
    });
    if (writable && response.body) {
      if (onProgress) {
        await pipeWithProgress(response, writable, onProgress);
      } else {
        await response.body.pipeTo(writable);
      }
      writable = null;
      return;
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = suggestedName;
    anchor.click();
    URL.revokeObjectURL(url);
  } finally {
    if (writable) await writable.abort();
  }
}

export function fileExportURL(authorization) {
  const url = new URL(apiURL("/api/v1/files/export"));
  url.searchParams.set("node_id", authorization.node_id);
  url.searchParams.set("resource_type", authorization.resource_type);
  url.searchParams.set("resource_id", authorization.resource_id);
  url.searchParams.set("path", authorization.path);
  if (runtimeConfig.proxySession) {
    url.searchParams.set("proxy_session", runtimeConfig.proxySession);
  }
  return url.toString();
}

async function withAuthorization(input, operation) {
  let grant = await authorize(input, proxyNodes.has(input.node_id));
  try {
    return await operation(grant);
  } catch (error) {
    if (grant.mode !== "direct" || !(error instanceof TypeError)) throw error;
    proxyNodes.add(input.node_id);
    if (mutatingScopes.has(input.scope)) {
      throw new ApiError("RESULT_UNKNOWN", "与节点的连接中断，请刷新目录确认操作结果后再重试", 0);
    }
    grant = await authorize(input, true);
    return operation(grant);
  }
}

async function authorize(input, forceProxy) {
  return request("/api/v1/files/authorize", {
    method: "POST",
    body: JSON.stringify({ ...input, force_proxy: forceProxy }),
  });
}

function fileHeaders(grant, extra = {}) {
  return {
    Authorization: `Bearer ${grant.ticket}`,
    "X-Prism-Resource-Type": grant.resource_type,
    "X-Prism-Resource-ID": grant.resource_id,
    ...extra,
  };
}

function endpointURL(grant) {
  const url = new URL(grant.mode === "proxy" ? apiURL(grant.endpoint) : grant.endpoint);
  url.searchParams.set("path", grant.path);
  return url.toString();
}

function grantHeaders(grant, extra = {}) {
  const headers = fileHeaders(grant, extra);
  return grant.mode === "proxy" ? runtimeHeaders(headers) : headers;
}
function credentialsFor(grant) {
  if (runtimeConfig.proxySession) return "omit";
  return grant.mode === "proxy" ? "same-origin" : "omit";
}

async function decodeJSON(response) {
  let payload;
  try {
    payload = await response.json();
  } catch {
    throw new ApiError("INVALID_RESPONSE", "节点返回了无法识别的响应", response.status);
  }
  if (!response.ok || !payload.success) {
    const error = payload.error || {};
    throw new ApiError(error.code || "REQUEST_FAILED", error.message || "文件操作失败", response.status);
  }
  return payload.data;
}

// ---- 字节级进度传输 ----

// XHR 上传：支持 upload.onprogress（{loaded, total}）。错误解析与 fetch 版保持一致：
// 网络错误 reject TypeError（触发 withAuthorization 的代理重试），HTTP/业务错误抛 ApiError。
// signal.abort() 时调用 xhr.abort()，以 AbortError 拒绝。
function xhrUpload(url, headers, body, onProgress, signal) {
  return new Promise((resolve, reject) => {
    if (signal && signal.aborted) {
      reject(abortError());
      return;
    }
    const xhr = new XMLHttpRequest();
    xhr.open("POST", url, true);
    for (const [key, value] of headerEntries(headers)) xhr.setRequestHeader(key, value);
    xhr.responseType = "json";
    if (onProgress && xhr.upload) {
      xhr.upload.onprogress = (event) => {
        if (!event.lengthComputable) return;
        onProgress({ loaded: event.loaded, total: event.total });
      };
    }
    xhr.onload = () => {
      try {
        resolve(decodeXHRResponse(xhr));
      } catch (error) {
        reject(error);
      }
    };
    xhr.onerror = () => reject(new TypeError("网络错误"));
    xhr.onabort = () => reject(abortError());
    if (signal) signal.addEventListener("abort", () => xhr.abort(), { once: true });
    xhr.send(body);
  });
}

function abortError() {
  const error = new Error("上传已取消");
  error.name = "AbortError";
  return error;
}

function decodeXHRResponse(xhr) {
  const payload = xhr.response;
  const status = xhr.status;
  if (!(status >= 200 && status < 300) || !payload || payload.success === false) {
    if (payload && payload.error) {
      const error = payload.error;
      throw new ApiError(error.code || "REQUEST_FAILED", error.message || "文件操作失败", status);
    }
    throw new ApiError("INVALID_RESPONSE", "节点返回了无法识别的响应", status);
  }
  return payload.data;
}

function headerEntries(headers) {
  if (typeof Headers !== "undefined" && headers instanceof Headers) {
    return Array.from(headers.entries());
  }
  return Object.entries(headers || {});
}

// 流式下载：逐块读 response.body 并写入 writable，每块回调进度。
// Content-Length 已知时计算百分比；未知时 percent 为 0（由调用方展示字节数）。
async function pipeWithProgress(response, writable, onProgress) {
  const contentLength = Number(response.headers.get("Content-Length")) || 0;
  const reader = response.body.getReader();
  const writer = writable.getWriter();
  let loaded = 0;
  const startedAt = performance.now();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      loaded += value.byteLength;
      onProgress({
        loaded,
        total: contentLength,
        percent: contentLength ? Math.min(100, Math.round((loaded / contentLength) * 100)) : 0,
        rate: loaded / Math.max(1, (performance.now() - startedAt) / 1000),
      });
      await writer.write(value);
    }
    await writer.close();
  } finally {
    writer.releaseLock();
  }
}
