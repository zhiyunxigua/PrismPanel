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

export async function savedAccounts() { return callWinApp("SavedAccounts"); }
export async function loginWinApp(username, password, remember) { return callWinApp("Login", username, password, Boolean(remember)); }
export async function loginSavedAccountWinApp(accountID) { return callWinApp("LoginSavedAccount", accountID); }
export async function deleteSavedAccountWinApp(accountID) { return callWinApp("DeleteSavedAccount", accountID); }
export async function updateSavedPasswordWinApp(username, password) { return callWinApp("UpdateSavedPassword", username, password); }

export async function netEaseAccount() { return callWinApp("NetEaseAccount"); }
export async function loginNetEaseAccount(email, password) { return callWinApp("LoginNetEaseAccount", email, password); }
export async function deleteNetEaseAccount() { return callWinApp("DeleteNetEaseAccount"); }
export async function gameVersions() { return callWinApp("GameVersions"); }
export async function gameServers() { return callWinApp("GameServers"); }
export async function createGameServer(input) { return callWinApp("CreateGameServer", input); }
export async function deleteGameServer(id) { return callWinApp("DeleteGameServer", id); }
export async function selectGameModDirectory() { return callWinApp("SelectGameModDirectory"); }
export async function joinGameServer(id) { return callWinApp("JoinGameServer", id); }
export async function joinGameServerConfig(input) { return callWinApp("JoinGameServerConfig", input); }
export async function gameJoinProgress(id) { return callWinApp("GameJoinProgress", id); }
export async function gameServerRunning(id) { return callWinApp("GameServerRunning", id); }

export async function prepareGameInstance(instanceDir) { return callWinApp("PrepareGameInstance", instanceDir); }
export async function checkNetEaseGameVersion(email, password, version) { return callWinApp("CheckNetEaseGameVersion", email, password, version); }
export async function downloadNetEaseGameVersion(email, password, version) { return callWinApp("DownloadNetEaseGameVersion", email, password, version); }
export async function checkSavedNetEaseGameVersion(version) { return callWinApp("CheckSavedNetEaseGameVersion", version); }
export async function downloadSavedNetEaseGameVersion(version) { return callWinApp("DownloadSavedNetEaseGameVersion", version); }

export function isWinApp() { return runtime.mode === "winapp"; }

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
    throw new Error("Windows 客户端服务不可用");
  }
  return operation(...args);
}