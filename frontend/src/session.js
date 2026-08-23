import { reactive } from "vue";
import { ApiError, request } from "./api.js";
import {
  isWinApp, loginSavedAccountWinApp, loginWinApp, runtimeConfig, updateSavedPasswordWinApp,
} from "./runtime.js";

export const sessionState = reactive({
  ready: false,
  initialized: true,
  user: null,
});

let bootstrapPromise;

export async function ensureSession() {
  if (sessionState.ready) return;
  if (bootstrapPromise) return bootstrapPromise;
  bootstrapPromise = (async () => {
    try {
      const status = await request("/api/v1/auth/status");
      sessionState.initialized = status.initialized;
      if (status.initialized) {
        const session = await request("/api/v1/auth/session");
        sessionState.user = session.user;
      }
    } catch (error) {
      const unauthenticated = error instanceof ApiError && error.status === 401;
      if (!unauthenticated && (!isWinApp() || !runtimeConfig.configured)) throw error;
      sessionState.user = null;
    }
    sessionState.ready = true;
  })().finally(() => {
    bootstrapPromise = null;
  });
  return bootstrapPromise;
}

export async function login(username, password, remember = false) {
  const data = isWinApp()
    ? await loginWinApp(username, password, remember)
    : await request("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    });
  applyLogin(data);
  return data;
}

export async function loginSavedAccount(accountID) {
  if (!isWinApp()) throw new Error("保存账号登录仅支持 Windows 客户端");
  const data = await loginSavedAccountWinApp(accountID);
  applyLogin(data);
  return data;
}

function applyLogin(data) {
  sessionState.user = data.user;
  sessionState.initialized = true;
}

export async function logout() {
  try {
    await request("/api/v1/auth/logout", { method: "POST", body: "{}" });
  } finally {
    sessionState.user = null;
  }
}

export async function changePassword(currentPassword, newPassword) {
  await request("/api/v1/auth/password", {
    method: "PUT",
    body: JSON.stringify({
      current_password: currentPassword,
      new_password: newPassword,
    }),
  });
  if (!isWinApp() || !sessionState.user?.username) return {};
  try {
    const updated = await updateSavedPasswordWinApp(sessionState.user.username, newPassword);
    return { savedCredentialUpdated: Boolean(updated) };
  } catch (error) {
    return {
      warning: "密码已更新，但 Windows 自动登录凭据更新失败：" + (error.message || "未知错误"),
    };
  }
}

export function hasPermission(permission) {
	const permissions = sessionState.user?.permissions || [];
	return permissions.includes("*") || permissions.includes(permission);
}

export function resetSession() {
  bootstrapPromise = undefined;
  sessionState.ready = false;
  sessionState.initialized = true;
  sessionState.user = null;
}

window.addEventListener("prism:session-expired", () => {
  sessionState.user = null;
});
