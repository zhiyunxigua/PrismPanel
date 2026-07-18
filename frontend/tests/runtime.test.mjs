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
  apiURL, deleteSavedAccountWinApp, isProxyURL, isWinApp, loginSavedAccountWinApp,
  loginWinApp, savedAccounts, updateSavedPasswordWinApp, websocketURL,
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
