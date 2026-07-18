import assert from "node:assert/strict";
import test from "node:test";

globalThis.window = {
  __PRISM_RUNTIME__: { mode: "web" },
  location: { href: "https://panel.example.test/login" },
  addEventListener() {},
  dispatchEvent() {},
};

let requestBody;
globalThis.fetch = async (_url, options) => {
  requestBody = JSON.parse(options.body);
  return new Response(JSON.stringify({
    success: true,
    data: { user: { username: "admin", permissions: [] }, initialized: false },
  }), { status: 200, headers: { "Content-Type": "application/json" } });
};

const { login, sessionState } = await import("../src/session.js");

test("browser login never sends or stores the remember flag", async () => {
  await login("admin", "secret", true);
  assert.deepEqual(requestBody, { username: "admin", password: "secret" });
  assert.equal(sessionState.user.username, "admin");
});
