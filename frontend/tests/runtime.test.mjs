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
  createGameServer,
  deleteGameServer,
  deleteNetEaseAccount,
  deleteSavedAccountWinApp,
  gameJoinProgress,
  gameServerRunning,
  gameServers,
  gameVersions,
  isProxyURL,
  isWinApp,
  joinGameServer,
  joinGameServerConfig,
  loginNetEaseAccount,
  loginSavedAccountWinApp,
  loginWinApp,
  netEaseAccount,
  savedAccounts,
  selectGameModDirectory,
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

test("WinApp game operations use the Wails bridge", async () => {
  const calls = [];
  window.go = { main: { App: {
    NetEaseAccount: async () => ({ email: "test@example.com" }),
    LoginNetEaseAccount: async (...args) => { calls.push(["netease-login", ...args]); return { email: args[0] }; },
    DeleteNetEaseAccount: async (...args) => { calls.push(["netease-delete", ...args]); },
    GameVersions: async () => [{ label: "1.20.6", version: 1020006, java: "jdk21" }],
    GameServers: async () => [{ id: "server-a", name: "local" }],
    CreateGameServer: async (...args) => { calls.push(["create-server", ...args]); return { id: "server-b" }; },
    DeleteGameServer: async (...args) => { calls.push(["delete-server", ...args]); return []; },
    SelectGameModDirectory: async (...args) => { calls.push(["select-dir", ...args]); return "F:/mods"; },
    GameServerRunning: async (...args) => { calls.push(["running", ...args]); return false; },
    JoinGameServer: async (...args) => { calls.push(["join", ...args]); return { status: "running" }; },
    JoinGameServerConfig: async (...args) => { calls.push(["join-config", ...args]); return { status: "running" }; },
    GameJoinProgress: async (...args) => { calls.push(["progress", ...args]); return { status: "done" }; },
  } } };

  assert.equal((await netEaseAccount()).email, "test@example.com");
  assert.equal((await gameVersions())[0].label, "1.20.6");
  assert.equal((await gameServers())[0].id, "server-a");
  await loginNetEaseAccount("test@example.com", "secret");
  await createGameServer({ name: "local" });
  await selectGameModDirectory();
  await gameServerRunning("server-a");
  await joinGameServer("server-a");
  await joinGameServerConfig({ game_id: "1001" });
  await gameJoinProgress("server-a");
  await deleteGameServer("server-a");
  await deleteNetEaseAccount();
  assert.deepEqual(calls, [
    ["netease-login", "test@example.com", "secret"],
    ["create-server", { name: "local" }],
    ["select-dir"],
    ["running", "server-a"],
    ["join", "server-a"],
    ["join-config", { game_id: "1001" }],
    ["progress", "server-a"],
    ["delete-server", "server-a"],
    ["netease-delete"],
  ]);
});