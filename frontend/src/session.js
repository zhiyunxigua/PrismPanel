import { reactive } from "vue";
import { ApiError, request } from "./api";

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
    const status = await request("/api/v1/auth/status");
    sessionState.initialized = status.initialized;
    if (status.initialized) {
      try {
        const session = await request("/api/v1/auth/session");
        sessionState.user = session.user;
      } catch (error) {
        if (!(error instanceof ApiError) || error.status !== 401) throw error;
      }
    }
    sessionState.ready = true;
  })().finally(() => {
    bootstrapPromise = null;
  });
  return bootstrapPromise;
}

export async function login(username, password) {
  const data = await request("/api/v1/auth/login", {
    method: "POST",
    body: JSON.stringify({ username, password }),
  });
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
}

export function hasPermission(permission) {
	const permissions = sessionState.user?.permissions || [];
	return permissions.includes("*") || permissions.includes(permission);
}

window.addEventListener("prism:session-expired", () => {
  sessionState.user = null;
});
