import { ApiError, request } from "./api";
import { apiURL, runtimeHeaders, runtimeConfig } from "./runtime";

const proxyNodes = new Set();
const mutatingScopes = new Set(["file.edit", "file.upload", "file.import", "file.create", "file.move", "file.delete"]);

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
  return withAuthorization(
    { ...authorization, size: file.size, overwrite },
    async (grant) => {
      const response = await fetch(endpointURL(grant), {
        method: "POST",
        headers: grantHeaders(grant, {
          "Content-Type": file.type || "application/octet-stream",
          "X-Prism-Overwrite": String(overwrite),
        }),
        body: file,
        credentials: credentialsFor(grant),
      });
      return decodeJSON(response);
    },
  );
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
    throw new ApiError(error.code || "REQUEST_FAILED", error.message || "文件操作失败", response.status);
  }
  return payload.data;
}
