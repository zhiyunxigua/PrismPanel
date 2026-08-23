import assert from "node:assert/strict";
import test from "node:test";

globalThis.window = {
  __PRISM_RUNTIME__: {
    mode: "winapp",
    configured: true,
    apiBaseUrl: "http://127.0.0.1:45123",
    proxySession: "local-session",
  },
  location: { href: "wails://wails.localhost/" },
};

const {
  apiURL,
  consoleWebSocketURL,
  checkWinAppUpdate,
  deleteSavedAccountWinApp,
  isProxyURL,
  isWinApp,
  installWinAppUpdate,
  loginSavedAccountWinApp,
  loginWinApp,
  runtimeConfig,
  savedAccounts,
  updateSavedPasswordWinApp,
  websocketURL,
} = await import("../src/runtime.js");

test("runtime exposes the WinApp marker", () => {
  assert.equal(isWinApp(), true);
});

test("relative API requests use the local Panel proxy", () => {
  assert.equal(apiURL("/api/v1/auth/session"), "http://127.0.0.1:45123/api/v1/auth/session");
  assert.equal(isProxyURL(apiURL("/api/v1/auth/session")), true);
});

test("local proxy WebSocket carries the local session", () => {
  assert.equal(
    websocketURL("/api/v1/ws/console?node_id=node-a"),
    "ws://127.0.0.1:45123/api/v1/ws/console?node_id=node-a&proxy_session=local-session",
  );
});

test("direct daemon WebSocket never receives the local session", () => {
  assert.equal(
    websocketURL("https://node.example.com/api/v1/ws/console"),
    "wss://node.example.com/api/v1/ws/console",
  );
  assert.equal(isProxyURL("https://node.example.com/api/v1/files/download"), false);
});

test("console WebSocket selects a browser-compatible transport", () => {
  const savedRuntime = { ...runtimeConfig };
  const savedLocation = window.location.href;
  for (const key of Object.keys(runtimeConfig)) delete runtimeConfig[key];
  try {
    window.location.href = "https://panel.example.com/servers/lobby";
    assert.equal(
      consoleWebSocketURL("http://node.example.com:24444/api/v1/ws/console", "node-a"),
      "wss://panel.example.com/api/v1/ws/console?node_id=node-a",
    );
    assert.equal(
      consoleWebSocketURL("https://node.example.com/api/v1/ws/console", "node-a"),
      "wss://node.example.com/api/v1/ws/console",
    );

    window.location.href = "http://panel.example.com/servers/lobby";
    assert.equal(
      consoleWebSocketURL("http://node.example.com:24444/api/v1/ws/console", "node-a"),
      "ws://node.example.com:24444/api/v1/ws/console",
    );
  } finally {
    window.location.href = savedLocation;
    Object.assign(runtimeConfig, savedRuntime);
  }
});

test("WinApp account operations use the Wails bridge", async () => {
  const calls = [];
  window.go = { main: { App: {
    SavedAccounts: async () => [{ id: "account-a", username: "admin" }],
    Login: async (...args) => { calls.push(["login", ...args]); return { user: { username: "admin" } }; },
    LoginSavedAccount: async (...args) => { calls.push(["saved", ...args]); return { user: { username: "admin" } }; },
    DeleteSavedAccount: async (...args) => { calls.push(["delete", ...args]); return []; },
    UpdateSavedPassword: async (...args) => { calls.push(["password", ...args]); return true; },
  } } };

  assert.equal((await savedAccounts())[0].username, "admin");
  await loginWinApp("admin", "secret", true);
  await loginSavedAccountWinApp("account-a");
  await deleteSavedAccountWinApp("account-a");
  await updateSavedPasswordWinApp("admin", "new-secret");
  assert.deepEqual(calls, [
    ["login", "admin", "secret", true],
    ["saved", "account-a"],
    ["delete", "account-a"],
    ["password", "admin", "new-secret"],
  ]);
});

test("WinApp client update operations use the Wails bridge", async () => {
  const calls = [];
  window.go = { main: { App: {
    CheckWinAppUpdate: async (...args) => { calls.push(["update-check", ...args]); return { update_available: false }; },
    InstallWinAppUpdate: async (...args) => { calls.push(["update-install", ...args]); },
  } } };

  await checkWinAppUpdate();
  await installWinAppUpdate("0.0.2");
  assert.deepEqual(calls, [
    ["update-check"],
    ["update-install", "0.0.2"],
  ]);
});
