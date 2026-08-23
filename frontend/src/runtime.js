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
export async function openRemoteFileWinApp(input, chooseApplication = false) { return callWinApp("OpenRemoteFile", input, Boolean(chooseApplication)); }
export async function fileOpenLimitWinApp() { return callWinApp("FileOpenLimit"); }

export async function checkWinAppUpdate() { return callWinApp("CheckWinAppUpdate"); }
export async function installWinAppUpdate(version) { return callWinApp("InstallWinAppUpdate", version); }
export async function selectMCGameDirectory() { return callWinApp("SelectMCGameDirectory"); }
export async function selectJavaExecutable() { return callWinApp("SelectJavaExecutable"); }

export async function mcAuthStatus() { return callWinApp("MCAuthStatus"); }
export async function mcStartDeviceLogin() { return callWinApp("MCStartDeviceLogin"); }
export async function mcPollDeviceLogin(stateID) { return callWinApp("MCPollDeviceLogin", stateID); }
export async function mcSetOfflineAccount(name) { return callWinApp("MCSetOfflineAccount", name); }
export async function mcLogout() { return callWinApp("MCLogout"); }
export async function mcAvailableVersions() { return callWinApp("MCAvailableVersions"); }
export async function mcFabricLoaders(gameVersion) { return callWinApp("MCFabricLoaders", gameVersion); }
export async function mcInstalledVersions() { return callWinApp("MCInstalledVersions"); }
export async function mcDeleteVersion(versionID) { return callWinApp("MCDeleteVersion", versionID); }
export async function mcIsFabricInstalled(gameVersion) { return callWinApp("MCIsFabricInstalled", gameVersion); }
export async function mcGetVersionSettings(versionID) { return callWinApp("MCGetVersionSettings", versionID); }
export async function mcSaveVersionSettings(versionID, settings) { return callWinApp("MCSaveVersionSettings", versionID, settings); }
export async function mcGetLauncherSettings() { return callWinApp("MCGetLauncherSettings"); }
export async function mcSaveLauncherSettings(settings) { return callWinApp("MCSaveLauncherSettings", settings); }
export async function mcThirdPartyLogin(server, username, password) { return callWinApp("MCThirdPartyLogin", server, username, password); }
export async function mcModsList(versionID) { return callWinApp("MCModsList", versionID); }
export async function mcModsToggle(versionID, filename, enabled) { return callWinApp("MCModsToggle", versionID, filename, enabled); }
export async function mcModsDelete(versionID, filename) { return callWinApp("MCModsDelete", versionID, filename); }
export async function mcModsOpenDir(versionID) { return callWinApp("MCModsOpenDir", versionID); }
export async function mcSearchModrinth(query, gameVersion, loader) { return callWinApp("MCSearchModrinth", query, gameVersion, loader); }
export async function mcModrinthInstall(versionID, projectID, gameVersion, loader) { return callWinApp("MCModrinthInstall", versionID, projectID, gameVersion, loader); }
export async function mcSetDevMode(on) { return callWinApp("MCSetDevMode", Boolean(on)); }
export async function mcDevModeEnabled() { return callWinApp("MCDevModeEnabled"); }
export async function mcDevLogList() { return callWinApp("MCDevLogList"); }
export async function mcDevLogClear() { return callWinApp("MCDevLogClear"); }
export async function mcOpenDevLog() { return callWinApp("MCOpenDevLog"); }
export async function mcDevLogPath() { return callWinApp("MCDevLogPath"); }
export async function mcInstallVersion(versionID) { return callWinApp("MCInstallVersion", versionID); }
export async function mcInstallFabric(gameVersion, loaderVersion) { return callWinApp("MCInstallFabric", gameVersion, loaderVersion); }
export async function mcAddDownload(kind, versionID, loader) { return callWinApp("MCAddDownload", kind, versionID, loader || ""); }
export async function mcDownloadList() { return callWinApp("MCDownloadList"); }
export async function mcDownloadActiveCount() { return callWinApp("MCDownloadActiveCount"); }
export async function mcCancelDownload(id) { return callWinApp("MCCancelDownload", id); }
export async function mcRemoveDownload(id) { return callWinApp("MCRemoveDownload", id); }
export async function mcClearDownloads() { return callWinApp("MCClearDownloads"); }
export async function mcLaunch(input) { return callWinApp("MCLaunch", input); }
export async function mcLaunchProgress(id) { return callWinApp("MCLaunchProgress", id); }
export async function mcCloseGame(id) { return callWinApp("MCCloseGame", id); }

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

export function consoleWebSocketURL(value, nodeID) {
  const target = new URL(websocketURL(value));
  const pageProtocol = new URL(window.location.href).protocol;
  if (pageProtocol === "https:" && target.protocol === "ws:") {
    return websocketURL(`/api/v1/ws/console?node_id=${encodeURIComponent(nodeID)}`);
  }
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

const devLogSkipMethods = new Set(["MCPushDevLog", "MCLaunchProgress", "MCPollDeviceLogin", "MCDevLogList", "MCDevLogPath", "MCDevModeEnabled"]);

function callWinApp(method, ...args) {
  const operation = window.go?.main?.App?.[method];
  if (typeof operation !== "function") {
    throw new Error("Windows 客户端服务不可用");
  }
  const started = performance.now();
  const result = operation(...args);
  if (!devLogSkipMethods.has(method) && devLoggingEnabled()) {
    let detail = method;
    const summary = args.map(summarizeArg).filter(Boolean).join(", ");
    if (summary) detail += `(${summary})`;
    const elapsedMs = Math.round(performance.now() - started);
    if (result && typeof result.then === "function") {
      result.then(
        () => pushDevLog(detail, elapsedMs, true, ""),
        (err) => pushDevLog(detail, elapsedMs, false, String(err?.message || err)),
      );
    } else {
      pushDevLog(detail, elapsedMs, true, "");
    }
  }
  return result;
}

let devLogging = null;
export function setDevLogging(value) {
  devLogging = Boolean(value);
}

function devLoggingEnabled() {
  return devLogging === true;
}

function summarizeArg(value) {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") {
    const trimmed = value.trim();
    return trimmed.length > 60 ? trimmed.slice(0, 57) + "..." : trimmed;
  }
  if (typeof value === "object") {
    try {
      const plain = {};
      for (const key of ["id", "version", "version_id", "name", "project_id", "game_version", "server_ip"]) {
        if (value[key] !== undefined && value[key] !== null && value[key] !== "") {
          plain[key] = String(value[key]);
        }
      }
      const text = JSON.stringify(plain);
      return text.length > 120 ? text.slice(0, 117) + "..." : text;
    } catch {
      return "[对象]";
    }
  }
  return String(value);
}

function pushDevLog(detail, elapsedMs, ok, errorText) {
  return callWinApp("MCPushDevLog", "ui", detail, elapsedMs, ok, errorText);
}
