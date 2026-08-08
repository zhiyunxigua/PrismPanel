const MINECRAFT_COLORS = Object.freeze({
  0: "000000",
  1: "0000aa",
  2: "00aa00",
  3: "00aaaa",
  4: "aa0000",
  5: "aa00aa",
  6: "ffaa00",
  7: "aaaaaa",
  8: "555555",
  9: "5555ff",
  a: "55ff55",
  b: "55ffff",
  c: "ff5555",
  d: "ff55ff",
  e: "ffff55",
  f: "ffffff",
});

const FORMAT_TOKEN = /\u00a7x(?:\u00a7[0-9a-f]){6}|\u00a7[0-9a-fk-or]/gi;
const LOG_PREFIX = /^\s*\[\d{1,2}:\d{2}:\d{2}(?:\s+([a-z]+))?\](?:\s+\[[^\]\r\n]*?\/([a-z]+)\])?:/i;
const LEVEL_COLORS = Object.freeze({
  TRACE: "8f9aa3",
  DEBUG: "aeb7bf",
  INFO: "ffffff",
  WARN: "ffd866",
  ERROR: "ff6b6b",
});

function ansiColor(hex) {
  const red = Number.parseInt(hex.slice(0, 2), 16);
  const green = Number.parseInt(hex.slice(2, 4), 16);
  const blue = Number.parseInt(hex.slice(4, 6), 16);
  return "\u001b[38;2;" + red + ";" + green + ";" + blue + "m";
}

export function minecraftToAnsi(value) {
  return String(value ?? "").replace(FORMAT_TOKEN, (token) => {
    if (token[1].toLowerCase() === "x") {
      return ansiColor(token.slice(2).replaceAll("\u00a7", ""));
    }
    const code = token[1].toLowerCase();
    if (MINECRAFT_COLORS[code]) return ansiColor(MINECRAFT_COLORS[code]);
    if (code === "l") return "\u001b[1m";
    if (code === "m") return "\u001b[9m";
    if (code === "n") return "\u001b[4m";
    if (code === "o") return "\u001b[3m";
    if (code === "r") return "\u001b[0m";
    return "";
  });
}

export function consoleLogLevel(value) {
  const match = String(value ?? "").match(LOG_PREFIX);
  const level = String(match?.[2] || match?.[1] || "").toUpperCase();
  if (level === "WARNING") return "WARN";
  if (level === "FATAL" || level === "SEVERE") return "ERROR";
  return LEVEL_COLORS[level] ? level : "";
}

export function consoleLineToAnsi(value) {
  const text = String(value ?? "");
  const color = LEVEL_COLORS[consoleLogLevel(text)];
  return (color ? ansiColor(color) : "") + minecraftToAnsi(text);
}
