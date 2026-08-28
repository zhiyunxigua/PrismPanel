import { ApiError, request } from "./api.js";
import { apiURL, runtimeHeaders, runtimeConfig } from "./runtime.js";

const proxyNodes = new Set();
const mutatingScopes = new Set(["file.edit", "file.upload", "file.import", "file.create", "file.move", "file.copy", "file.archive", "file.extract", "file.delete", "file.recycle.restore", "file.recycle.delete", "file.recycle.clear"]);

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

export async function uploadFile(authorization, file, overwrite = false, options = {}) {
  const uploadId = options.uploadId || createUploadID();
  const authorizeInput = { ...authorization, size: file.size, overwrite, chunked: true };
  let forceProxy = proxyNodes.has(authorization.node_id);
  let grant = await authorize(authorizeInput, forceProxy);
  let offset = Number(options.offset) || 0;
  let attempts = 0;
  let finalizePending = file.size === 0;

  if (options.resume === true) {
    const status = await uploadStatus(authorization, uploadId);
    if (status.complete) {
      options.onProgress?.({ loaded: file.size, total: file.size, uploadId });
      return status.entry;
    }
    offset = Math.min(Number(status.offset) || 0, file.size);
    finalizePending = offset === file.size;
  }
  const reportProgress = throttledProgress(options.onProgress, options.progressInterval, file.size, uploadId);
  reportProgress(offset, true);

  while (offset < file.size || finalizePending) {
    throwIfAborted(options.signal);
    const chunkSize = Math.max(1, Number(grant.chunk_size) || 2 * 1024 * 1024);
    const end = Math.min(offset + chunkSize, file.size);
    const final = end === file.size;
    const chunk = file.slice(offset, end, file.type || "application/octet-stream");
    try {
      const result = await uploadChunk(grant, chunk, {
        uploadId, offset, final, overwrite, signal: options.signal,
        onProgress: (loaded) => reportProgress(Math.min(offset + loaded, file.size)),
      });
      offset = final ? file.size : Number(result.offset);
      finalizePending = false;
      if (!Number.isFinite(offset) || offset < 0 || offset > file.size) {
        throw new ApiError("INVALID_RESPONSE", "节点返回了无效的上传偏移", 0);
      }
      reportProgress(offset, true);
      attempts = 0;
      if (final) return result;
      if (file.size === 0) break;
    } catch (error) {
      if (isAbortError(error) || options.signal?.aborted) throw error;
      const retryable = isRetryableUploadError(error);
      if (!retryable || attempts >= 4) throw error;
      attempts += 1;
      if (grant.mode === "direct") {
        proxyNodes.add(authorization.node_id);
        forceProxy = true;
      }
      await retryDelay(attempts, options.signal);
      grant = await authorize(authorizeInput, forceProxy);
      const status = await uploadStatus(authorization, uploadId);
      if (status.complete) {
        reportProgress(file.size, true);
        return status.entry;
      }
      offset = Math.min(Number(status.offset) || 0, file.size);
      finalizePending = offset === file.size;
      reportProgress(offset, true);
    }
  }
  throw new ApiError("UPLOAD_INCOMPLETE", "文件上传未完成", 0, "", { retryable: true });
}

function throttledProgress(callback, interval = 100, total, uploadId) {
  let lastReportedAt = 0;
  return (loaded, force = false) => {
    if (!callback) return;
    const now = Date.now();
    if (!force && now - lastReportedAt < Math.max(0, interval)) return;
    lastReportedAt = now;
    callback({ loaded, total, uploadId });
  };
}

export function uploadStatus(authorization, uploadId) {
  return fileJSON({ ...authorization, scope: "file.upload.status" }, "POST", { upload_id: uploadId });
}

export function cancelUpload(authorization, uploadId) {
  return fileJSON({ ...authorization, scope: "file.upload.cancel" }, "POST", { upload_id: uploadId });
}

export function listRecycleBin(authorization) {
  return fileJSON({ ...authorization, scope: "file.recycle.list", path: "." }, "POST", {});
}

export function restoreRecycleEntry(authorization, id) {
  return fileJSON({ ...authorization, scope: "file.recycle.restore", path: "." }, "POST", { id });
}

export function deleteRecycleEntries(authorization, ids) {
  return fileJSON({ ...authorization, scope: "file.recycle.delete", path: "." }, "POST", { ids });
}

export function clearRecycleBin(authorization) {
  return fileJSON({ ...authorization, scope: "file.recycle.clear", path: "." }, "POST", {});
}

export function createUploadID() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  const random = globalThis.crypto?.getRandomValues?.(new Uint8Array(16));
  if (random) return Array.from(random, (value) => value.toString(16).padStart(2, "0")).join("");
  return `upload-${Date.now()}-${Math.random().toString(36).slice(2)}`;
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

export async function downloadFile(authorization, suggestedName) {
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
      await response.body.pipeTo(writable);
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

function uploadChunk(grant, chunk, options) {
  if (typeof XMLHttpRequest === "undefined") {
    return uploadChunkWithFetch(grant, chunk, options);
  }
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    request.open("POST", endpointURL(grant));
    new Headers(grantHeaders(grant, uploadChunkHeaders(chunk, options)))
      .forEach((value, name) => request.setRequestHeader(name, value));
    request.withCredentials = credentialsFor(grant) === "same-origin";
    request.upload.onprogress = (event) => options.onProgress?.(event.loaded);
    request.onerror = () => reject(new TypeError("上传连接中断"));
    request.onabort = () => reject(new DOMException("上传已暂停", "AbortError"));
    request.onload = async () => {
      const response = new Response(request.responseText, {
        status: request.status,
        statusText: request.statusText,
        headers: parseXHRHeaders(request.getAllResponseHeaders()),
      });
      try {
        resolve(await decodeJSON(response));
      } catch (error) {
        reject(error);
      }
    };
    const abort = () => request.abort();
    options.signal?.addEventListener("abort", abort, { once: true });
    request.addEventListener("loadend", () => options.signal?.removeEventListener("abort", abort));
    request.send(chunk);
  });
}

async function uploadChunkWithFetch(grant, chunk, options) {
  const response = await fetch(endpointURL(grant), {
    method: "POST",
    headers: grantHeaders(grant, uploadChunkHeaders(chunk, options)),
    body: chunk,
    signal: options.signal,
    credentials: credentialsFor(grant),
  });
  return decodeJSON(response);
}

function uploadChunkHeaders(chunk, options) {
  return {
    "Content-Type": chunk.type || "application/octet-stream",
    "X-Prism-Overwrite": String(options.overwrite),
    "X-Prism-Upload-ID": options.uploadId,
    "X-Prism-Upload-Offset": String(options.offset),
    "X-Prism-Upload-Final": String(options.final),
  };
}

function isRetryableUploadError(error) {
  if (error instanceof TypeError) return true;
  if (!(error instanceof ApiError)) return false;
  return error.retryable || error.status === 0 || error.status === 408 || error.status === 425 ||
    error.status === 429 || [502, 503, 504].includes(error.status) || error.code === "TICKET_EXPIRED" ||
    error.code === "UNAUTHENTICATED" || error.code === "UPLOAD_OFFSET_MISMATCH";
}

function parseXHRHeaders(raw) {
  const headers = new Headers();
  for (const line of String(raw || "").trim().split(/[\r\n]+/)) {
    const index = line.indexOf(":");
    if (index > 0) headers.append(line.slice(0, index).trim(), line.slice(index + 1).trim());
  }
  return headers;
}

function throwIfAborted(signal) {
  if (!signal?.aborted) return;
  throw signal.reason instanceof Error ? signal.reason : new DOMException("上传已暂停", "AbortError");
}

function isAbortError(error) {
  return error?.name === "AbortError";
}

function retryDelay(attempt, signal) {
  return new Promise((resolve, reject) => {
    const onAbort = () => {
      clearTimeout(timer);
      reject(new DOMException("上传已暂停", "AbortError"));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, Math.min(500 * (2 ** (attempt - 1)), 4000));
    signal?.addEventListener("abort", onAbort, { once: true });
  });
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
    "X-Prism-Path": grant.path,
    ...extra,
  };
}

function endpointURL(grant) {
  return grant.mode === "proxy" ? apiURL(grant.endpoint) : grant.endpoint;
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
    throw new ApiError(
      error.code || "REQUEST_FAILED",
      error.message || "文件操作失败",
      response.status,
      payload.request_id || response.headers.get("X-Request-ID") || "",
      error,
    );
  }
  return payload.data;
}
