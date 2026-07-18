import assert from "node:assert/strict";
import test from "node:test";

globalThis.window = {
  __PRISM_RUNTIME__: {
    mode: "winapp",
    configured: true,
    apiBaseUrl: "http://127.0.0.1:45123",
    proxySession: "local-session",
    connectionError: "远程 Panel 当前不可用",
  },
  location: { href: "wails://wails.localhost/" },
  addEventListener() {},
  dispatchEvent() {},
};

globalThis.fetch = async () => new Response(JSON.stringify({
  success: false,
  error: { code: "UPSTREAM_UNAVAILABLE", message: "远程面板连接失败" },
}), { status: 502, headers: { "Content-Type": "application/json" } });

const { ensureSession, sessionState } = await import("../src/session.js");

test("configured WinApp degrades to login when the saved Panel is offline", async () => {
  await ensureSession();
  assert.equal(sessionState.ready, true);
  assert.equal(sessionState.user, null);
  assert.equal(sessionState.initialized, true);
});
