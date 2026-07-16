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
  const headers = new Headers(options.headers || {});
  if (options.body !== undefined && !(options.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(path, {
    ...options,
    headers,
    credentials: "same-origin",
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
    if (response.status === 401) {
      window.dispatchEvent(new CustomEvent("prism:session-expired"));
    }
    throw apiError;
  }
  return payload.data;
}
