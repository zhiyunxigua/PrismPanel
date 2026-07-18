const runtime = { ...(window.__PRISM_RUNTIME__ || {}) };

export const runtimeConfig = runtime;

export async function initializeRuntime() {
  const app = window.go?.main?.App;
  if (typeof app?.RuntimeConfig !== "function") return runtime;
  return replaceRuntime(await app.RuntimeConfig());
}

export async function configurePanelURL(panelURL) {
  const app = window.go?.main?.App;
  if (typeof app?.ConfigurePanelURL !== "function") {
    throw new Error("Windows 客户端连接服务不可用");
  }
  return replaceRuntime(await app.ConfigurePanelURL(panelURL));
}

export async function savedAccounts() {
  return callWinApp("SavedAccounts");
}

export async function loginWinApp(username, password, remember) {
  return callWinApp("Login", username, password, Boolean(remember));
}

export async function loginSavedAccountWinApp(accountID) {
  return callWinApp("LoginSavedAccount", accountID);
}

export async function deleteSavedAccountWinApp(accountID) {
  return callWinApp("DeleteSavedAccount", accountID);
}

export async function updateSavedPasswordWinApp(username, password) {
  return callWinApp("UpdateSavedPassword", username, password);
}

export function isWinApp() {
  return runtime.mode === "winapp";
}

export function apiURL(path) {
  const base = runtime.apiBaseUrl || window.location.href;
  return new URL(path, base).toString();
}

export function runtimeHeaders(input) {
  const headers = new Headers(input || {});
  if (runtime.proxySession) {
    headers.set("X-Prism-Client-Session", runtime.proxySession);
  }
  return headers;
}

export function websocketURL(value) {
  const target = new URL(value, runtime.apiBaseUrl || window.location.href);
  if (isProxyURL(target) && runtime.proxySession) {
    target.searchParams.set("proxy_session", runtime.proxySession);
  }
  if (target.protocol === "http:") target.protocol = "ws:";
  if (target.protocol === "https:") target.protocol = "wss:";
  return target.toString();
}

export function isProxyURL(value) {
  if (!runtime.apiBaseUrl) return false;
  const target = value instanceof URL ? value : new URL(value, runtime.apiBaseUrl);
  const proxy = new URL(runtime.apiBaseUrl, window.location.href);
  return target.origin === proxy.origin;
}

function replaceRuntime(value) {
  for (const key of Object.keys(runtime)) delete runtime[key];
  Object.assign(runtime, value || {});
  window.__PRISM_RUNTIME__ = runtime;
  return runtime;
}

function callWinApp(method, ...args) {
  const operation = window.go?.main?.App?.[method];
  if (typeof operation !== "function") {
    throw new Error("Windows 客户端登录服务不可用");
  }
  return operation(...args);
}
