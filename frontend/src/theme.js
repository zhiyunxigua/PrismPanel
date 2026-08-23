import { readonly, ref } from "vue";

const STORAGE_KEY = "prismpanel.theme";
const THEMES = new Set(["light", "dark"]);

function preferredTheme() {
  const storedTheme = window.localStorage.getItem(STORAGE_KEY);
  if (THEMES.has(storedTheme)) return storedTheme;
  if (window.matchMedia) {
    const systemTheme = window.matchMedia("(prefers-color-scheme: dark)");
    if (systemTheme.matches) return "dark";
  }
  return "light";
}

const theme = ref(preferredTheme());

function applyTheme(nextTheme) {
  document.documentElement.classList.toggle("dark", nextTheme === "dark");
  document.documentElement.dataset.theme = nextTheme;
  document.documentElement.style.colorScheme = nextTheme;
}

export const currentTheme = readonly(theme);

export function initializeTheme() {
  applyTheme(theme.value);
}

export function setTheme(nextTheme) {
  if (!THEMES.has(nextTheme) || nextTheme === theme.value) return;
  theme.value = nextTheme;
  window.localStorage.setItem(STORAGE_KEY, nextTheme);
  applyTheme(nextTheme);
}

export function toggleTheme() {
  if (theme.value === "dark") setTheme("light");
  else setTheme("dark");
}
