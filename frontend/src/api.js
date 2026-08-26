import { apiURL, runtimeHeaders, runtimeConfig } from "./runtime.js";

export class ApiError extends Error {
  constructor(code, message, status, requestId = "") {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
    this.requestId = requestId;
  }
}

export async function request(path, options = {}) {
  const headers = runtimeHeaders(options.headers || {});
  if (options.body !== undefined && !(options.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(apiURL(path), {
    ...options,
    headers,
    credentials: runtimeConfig.proxySession ? "omit" : "same-origin",
  });
  let payload;
  try {
    payload = await response.json();
  } catch {
    throw new ApiError("INVALID_RESPONSE", "服务器返回了无法识别的响应", response.status);
  }
  if (!response.ok || !payload.success) {
    const error = payload.error || {};
    const apiError = new ApiError(
      error.code || "REQUEST_FAILED",
      error.message || "请求失败",
      response.status,
      payload.request_id || response.headers.get("X-Request-ID") || "",
    );
    apiError.data = payload.data;
    if (response.status === 401) {
      window.dispatchEvent(new CustomEvent("prism:session-expired"));
    }
    throw apiError;
  }
  return payload.data;
}

// XHR 版 request：通过 xhr.upload.onprogress 提供字节级上传进度（{loaded, total}），
// 错误处理与 request 一致（ApiError、401 会话过期事件、apiError.data 透传）。
export function requestWithProgress(path, options = {}, onProgress) {
  const headers = runtimeHeaders(options.headers || {});
  if (options.body !== undefined && !(options.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open(options.method || "GET", apiURL(path), true);
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
        const payload = xhr.response;
        const status = xhr.status;
        if (!(status >= 200 && status < 300) || !payload || payload.success === false) {
          const error = payload && payload.error ? payload.error : {};
          const apiError = new ApiError(
            error.code || "REQUEST_FAILED",
            error.message || "请求失败",
            status,
            (payload && payload.request_id) || xhr.getResponseHeader("X-Request-ID") || "",
          );
          apiError.data = payload ? payload.data : undefined;
          if (status === 401) {
            window.dispatchEvent(new CustomEvent("prism:session-expired"));
          }
          throw apiError;
        }
        resolve(payload.data);
      } catch (error) {
        reject(error);
      }
    };
    xhr.onerror = () => reject(new TypeError("网络错误"));
    xhr.send(options.body);
  });
}

function headerEntries(headers) {
  if (typeof Headers !== "undefined" && headers instanceof Headers) {
    return Array.from(headers.entries());
  }
  return Object.entries(headers || {});
}
