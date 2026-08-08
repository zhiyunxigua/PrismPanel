<script setup>
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { ElMessage } from "element-plus";
import { Eraser, RefreshCw, Send } from "lucide-vue-next";
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { request } from "../../api";
import { consoleWebSocketURL } from "../../runtime";
import { addCommandHistory, navigateCommandHistory } from "./console-history";
import { consoleLineToAnsi } from "./minecraft-format";

const props = defineProps({
  nodeId: { type: String, required: true },
  instanceId: { type: String, required: true },
  enabled: { type: Boolean, default: false },
  canCommand: { type: Boolean, default: false },
  running: { type: Boolean, default: false },
});

const HISTORY_STORAGE_KEY = "prism.console.command-history";
const PROMPT = "\u001b[38;2;118;196;154m> \u001b[0m";
const RESET_LINE = "\u001b[0m\r\n";

const terminalRef = ref();
const status = ref("disconnected");
const statusMessage = ref("");
const command = ref("");
const sending = ref(false);
const autoScroll = ref(true);
const commandHistory = ref(loadCommandHistory());

let terminal;
let fitAddon;
let resizeObserver;
let inputDisposable;
let scrollDisposable;
let socket;
let reconnectTimer;
let outputFrame;
let pendingOutput = "";
let generation = 0;
let lastSequence = 0;
let currentSessionId = "";
let directInput = "";
let promptVisible = false;
let commandNavigation = emptyNavigation();
let directNavigation = emptyNavigation();

function emptyNavigation() {
  return { index: -1, draft: "" };
}

function loadCommandHistory() {
  try {
    const value = JSON.parse(window.localStorage.getItem(HISTORY_STORAGE_KEY) || "[]");
    return Array.isArray(value) ? value.filter((item) => typeof item === "string") : [];
  } catch {
    return [];
  }
}

function rememberCommand(value) {
  commandHistory.value = addCommandHistory(commandHistory.value, value);
  try {
    window.localStorage.setItem(HISTORY_STORAGE_KEY, JSON.stringify(commandHistory.value));
  } catch {
    // 浏览器禁用本地存储时，当前页面内的历史仍然可用。
  }
}

function canUseTerminalInput() {
  return props.canCommand && props.running && status.value === "connected";
}

function initTerminal() {
  terminal = new Terminal({
    convertEol: true,
    cursorBlink: true,
    cursorStyle: "underline",
    disableStdin: !canUseTerminalInput(),
    fontFamily: 'Consolas, "Cascadia Mono", "Courier New", monospace',
    fontSize: 12,
    letterSpacing: 0,
    lineHeight: 1,
    scrollback: 2000,
    theme: {
      background: "#171b1f",
      foreground: "#d8dee9",
      cursor: "#76c49a",
      cursorAccent: "#171b1f",
      selectionBackground: "#45645599",
      black: "#000000",
      red: "#aa0000",
      green: "#00aa00",
      yellow: "#ffaa00",
      blue: "#0000aa",
      magenta: "#aa00aa",
      cyan: "#00aaaa",
      white: "#aaaaaa",
      brightBlack: "#555555",
      brightRed: "#ff5555",
      brightGreen: "#55ff55",
      brightYellow: "#ffff55",
      brightBlue: "#5555ff",
      brightMagenta: "#ff55ff",
      brightCyan: "#55ffff",
      brightWhite: "#ffffff",
    },
  });
  fitAddon = new FitAddon();
  terminal.loadAddon(fitAddon);
  terminal.open(terminalRef.value);
  terminal.textarea?.setAttribute("aria-label", "控制台终端");
  terminal.attachCustomKeyEventHandler(handleTerminalKeyEvent);
  inputDisposable = terminal.onData(handleTerminalInput);
  scrollDisposable = terminal.onScroll(handleTerminalScroll);
  resizeObserver = new ResizeObserver(fitTerminal);
  resizeObserver.observe(terminalRef.value);
  nextTick(() => {
    fitTerminal();
    syncTerminalInput();
  });
}

function fitTerminal() {
  const element = terminalRef.value;
  if (!fitAddon || !element?.clientWidth || !element?.clientHeight) return;
  try {
    fitAddon.fit();
  } catch {
    // 页签切换过程中容器可能短暂不可测量，ResizeObserver 会再次触发。
  }
}

function stopConnection() {
  generation += 1;
  window.clearTimeout(reconnectTimer);
  reconnectTimer = undefined;
  if (socket) {
    socket.onclose = null;
    socket.close();
    socket = undefined;
  }
}

function scheduleReconnect(expectedGeneration) {
  if (!props.enabled || expectedGeneration !== generation) return;
  status.value = "disconnected";
  syncTerminalInput();
  reconnectTimer = window.setTimeout(() => connect(expectedGeneration), 3000);
}

async function connect(expectedGeneration = generation) {
  if (!props.enabled || !props.nodeId || !props.instanceId || expectedGeneration !== generation) return;
  status.value = "connecting";
  statusMessage.value = "";
  syncTerminalInput();
  try {
    const ticket = await request(
      `/api/v1/instances/${encodeURIComponent(props.instanceId)}/console-ticket?node_id=${encodeURIComponent(props.nodeId)}`,
      { method: "POST", body: "{}" },
    );
    if (expectedGeneration !== generation) return;
    const current = new WebSocket(consoleWebSocketURL(ticket.websocket_url, props.nodeId));
    socket = current;
    current.onopen = () => {
      current.send(JSON.stringify({
        type: "auth",
        ticket: ticket.ticket,
        instance_id: props.instanceId,
        after_sequence: lastSequence,
      }));
    };
    current.onmessage = (event) => {
      let message;
      try {
        message = JSON.parse(event.data);
      } catch {
        return;
      }
      if (message.type === "auth.result") {
        if (message.success) {
          status.value = "connected";
          statusMessage.value = "";
        } else {
          status.value = "error";
          statusMessage.value = message.error?.message || "控制台鉴权失败";
        }
        syncTerminalInput();
        return;
      }
      if (message.type === "proxy.error") {
        status.value = "error";
        statusMessage.value = message.message || "控制台代理连接失败";
        syncTerminalInput();
        return;
      }
      if (message.type === "console.snapshot") applySnapshot(message);
      if (message.type === "console.reset") resetOutput(message);
      if (message.type === "console.line") appendLine(message);
    };
    current.onerror = () => {
      status.value = "error";
      statusMessage.value = "控制台连接失败";
      syncTerminalInput();
    };
    current.onclose = () => {
      if (socket === current) socket = undefined;
      scheduleReconnect(expectedGeneration);
    };
  } catch (error) {
    status.value = "error";
    statusMessage.value = error.message;
    syncTerminalInput();
    scheduleReconnect(expectedGeneration);
  }
}

function formatLine(line) {
  if (line?.type !== "console.line") return "";
  return consoleLineToAnsi(String(line.content ?? "")) + RESET_LINE;
}

function applySnapshot(snapshot) {
  const sessionId = String(snapshot.session_id ?? "");
  const replace = !currentSessionId
    || sessionId !== currentSessionId
    || Boolean(snapshot.truncated)
    || lastSequence === 0;
  if (replace) {
    clearTerminalOutput();
    lastSequence = 0;
  }
  if (sessionId) currentSessionId = sessionId;
  lastSequence = Math.max(lastSequence, Number(snapshot.sequence) || 0);
  const output = Array.isArray(snapshot.lines) ? snapshot.lines.map(formatLine).join("") : "";
  queueTerminalOutput(output);
}

function appendLine(line) {
  const sequence = Number(line.sequence) || 0;
  if (sequence && sequence <= lastSequence) return;
  const sessionId = String(line.session_id ?? "");
  if (sessionId && currentSessionId && sessionId !== currentSessionId) clearTerminalOutput();
  if (sessionId) currentSessionId = sessionId;
  lastSequence = Math.max(lastSequence, sequence);
  queueTerminalOutput(formatLine(line));
}

function resetOutput(event) {
  const sessionId = String(event.session_id ?? "");
  const sessionChanged = Boolean(sessionId && sessionId !== currentSessionId);
  currentSessionId = sessionId;
  lastSequence = sessionChanged
    ? Number(event.sequence) || 0
    : Math.max(lastSequence, Number(event.sequence) || 0);
  clearTerminalOutput();
}

function queueTerminalOutput(value) {
  if (!value) return;
  pendingOutput += value;
  if (outputFrame) return;
  outputFrame = window.requestAnimationFrame(flushTerminalOutput);
}

function flushTerminalOutput() {
  outputFrame = undefined;
  if (!terminal || !pendingOutput) return;
  const output = pendingOutput;
  const viewport = terminal.buffer.active.viewportY;
  const shouldFollow = autoScroll.value;
  pendingOutput = "";
  erasePrompt();
  terminal.write(output, () => {
    if (shouldFollow) terminal.scrollToBottom();
    else terminal.scrollToLine(viewport);
    renderPrompt();
  });
}

function clearPendingOutput() {
  if (outputFrame) window.cancelAnimationFrame(outputFrame);
  outputFrame = undefined;
  pendingOutput = "";
}

function clearTerminalOutput() {
  clearPendingOutput();
  directInput = "";
  directNavigation = emptyNavigation();
  promptVisible = false;
  if (!terminal) return;
  terminal.clear();
  terminal.write("\u001b[2J\u001b[H", renderPrompt);
}

function erasePrompt() {
  if (!terminal || !promptVisible) return;
  terminal.write("\r\u001b[2K");
  promptVisible = false;
}

function renderPrompt() {
  if (!terminal) return;
  if (!canUseTerminalInput()) {
    erasePrompt();
    return;
  }
  terminal.write("\r\u001b[2K" + PROMPT + terminalSafeText(directInput));
  promptVisible = true;
  if (autoScroll.value) terminal.scrollToBottom();
}

function syncTerminalInput() {
  if (!terminal) return;
  terminal.options.disableStdin = !canUseTerminalInput();
  if (canUseTerminalInput()) renderPrompt();
  else {
    erasePrompt();
    terminal.blur();
  }
}

function terminalSafeText(value) {
  return String(value ?? "").replace(/[\u0000-\u001f\u007f-\u009f]/g, "");
}

function handleTerminalScroll(position) {
  if (!terminal) return;
  autoScroll.value = position >= terminal.buffer.active.baseY;
}

function handleTerminalKeyEvent(event) {
  if (event.ctrlKey && !event.altKey && !event.metaKey && event.key.toLowerCase() === "c") {
    return false;
  }
  return true;
}

function handleTerminalInput(data) {
  if (!canUseTerminalInput()) return;
  autoScroll.value = true;
  terminal.scrollToBottom();
  if (data === "\r") {
    submitDirectCommand();
    return;
  }
  if (data === "\u007f") {
    directInput = Array.from(directInput).slice(0, -1).join("");
    directNavigation = emptyNavigation();
    renderPrompt();
    return;
  }
  if (data === "\u001b[A" || data === "\u001b[B") {
    const result = navigateCommandHistory(
      commandHistory.value,
      directNavigation,
      data === "\u001b[A" ? -1 : 1,
      directInput,
    );
    directNavigation = result.navigation;
    directInput = result.value;
    renderPrompt();
    return;
  }
  const printable = terminalSafeText(data);
  if (!printable || directInput.length + printable.length > 8192) return;
  directInput += printable;
  directNavigation = emptyNavigation();
  renderPrompt();
}

function submitDirectCommand() {
  if (sending.value) return;
  const value = directInput.trim();
  terminal.write("\r\u001b[2K" + PROMPT + terminalSafeText(directInput) + "\r\n");
  promptVisible = false;
  directInput = "";
  directNavigation = emptyNavigation();
  if (!value) {
    renderPrompt();
    return;
  }
  executeCommand(value);
}

async function sendCommand() {
  const value = command.value.trim();
  if (!value || sending.value || !props.canCommand) return;
  echoCommand(value);
  const success = await executeCommand(value);
  if (success) command.value = "";
  commandNavigation = emptyNavigation();
}

function echoCommand(value) {
  if (!terminal) return;
  erasePrompt();
  terminal.write(PROMPT + terminalSafeText(value) + "\r\n", renderPrompt);
}

async function executeCommand(value) {
  if (!value || sending.value || !props.canCommand || !props.running) return false;
  sending.value = true;
  rememberCommand(value);
  try {
    await request(
      `/api/v1/instances/${encodeURIComponent(props.instanceId)}/command?node_id=${encodeURIComponent(props.nodeId)}`,
      { method: "POST", body: JSON.stringify({ command: value }) },
    );
    return true;
  } catch (error) {
    ElMessage.error(error.message);
    return false;
  } finally {
    sending.value = false;
    renderPrompt();
  }
}

function handleCommandKeydown(event) {
  if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
  event.preventDefault();
  const result = navigateCommandHistory(
    commandHistory.value,
    commandNavigation,
    event.key === "ArrowUp" ? -1 : 1,
    command.value,
  );
  commandNavigation = result.navigation;
  command.value = result.value;
}

function resetCommandNavigation() {
  commandNavigation = emptyNavigation();
}

function reconnect() {
  stopConnection();
  connect(generation);
}

function resetConsoleContext() {
  stopConnection();
  clearTerminalOutput();
  lastSequence = 0;
  currentSessionId = "";
  status.value = props.enabled ? "connecting" : "disconnected";
  statusMessage.value = "";
  syncTerminalInput();
  if (props.enabled) connect(generation);
}

watch(
  () => [props.nodeId, props.instanceId, props.enabled],
  () => {
    if (terminal) resetConsoleContext();
  },
);

watch(
  () => [props.canCommand, props.running],
  syncTerminalInput,
);

watch(autoScroll, (enabled) => {
  if (enabled) terminal?.scrollToBottom();
});

onMounted(() => {
  initTerminal();
  resetConsoleContext();
});

onBeforeUnmount(() => {
  stopConnection();
  clearPendingOutput();
  resizeObserver?.disconnect();
  inputDisposable?.dispose();
  scrollDisposable?.dispose();
  terminal?.dispose();
  terminal = undefined;
});
</script>

<template>
  <section class="console-tool">
    <div class="console-toolbar">
      <div class="console-status" :class="status">
        <span />
        <strong>{{ status === "connected" ? "已连接" : status === "connecting" ? "连接中" : status === "error" ? "连接异常" : "未连接" }}</strong>
        <small v-if="statusMessage">{{ statusMessage }}</small>
      </div>
      <div class="console-actions">
        <el-checkbox v-model="autoScroll">自动滚动</el-checkbox>
        <el-tooltip content="重新连接">
          <button class="console-icon-button" type="button" aria-label="重新连接" @click="reconnect">
            <RefreshCw :size="15" />
          </button>
        </el-tooltip>
        <el-tooltip content="清空显示">
          <button class="console-icon-button" type="button" aria-label="清空显示" @click="clearTerminalOutput">
            <Eraser :size="15" />
          </button>
        </el-tooltip>
      </div>
    </div>
    <div ref="terminalRef" class="console-terminal" />
    <form v-if="canCommand" class="console-command" @submit.prevent="sendCommand">
      <el-input
        v-model="command"
        :disabled="!running"
        maxlength="8192"
        placeholder="输入控制台命令"
        @input="resetCommandNavigation"
        @keydown="handleCommandKeydown"
      />
      <el-button type="primary" native-type="submit" :loading="sending" :disabled="!running || !command.trim()">
        <Send v-if="!sending" :size="15" />
        发送
      </el-button>
    </form>
  </section>
</template>

<style scoped>
.console-tool {
  overflow: hidden;
  background: #171b1f;
  border: 1px solid #303840;
  border-radius: 6px;
}
.console-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 46px;
  padding: 7px 10px 7px 13px;
  color: #b8c0c7;
  background: #22282d;
  border-bottom: 1px solid #343b42;
}
.console-status, .console-actions {
  display: flex;
  align-items: center;
  min-width: 0;
}
.console-status { gap: 7px; }
.console-status > span {
  width: 7px;
  height: 7px;
  flex: 0 0 auto;
  background: #77818a;
  border-radius: 50%;
}
.console-status.connected > span { background: #48b779; }
.console-status.connecting > span { background: #d5a33d; }
.console-status.error > span { background: #d86157; }
.console-status strong { font-size: 11px; }
.console-status small {
  max-width: min(460px, 45vw);
  overflow: hidden;
  color: #8f9aa3;
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.console-actions { gap: 6px; }
.console-actions :deep(.el-checkbox__label) { color: #aeb7bf; font-size: 11px; }
.console-icon-button {
  display: inline-grid;
  place-items: center;
  width: 30px;
  height: 30px;
  color: #b7c0c8;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 4px;
}
.console-icon-button:hover { color: #fff; background: #303840; border-color: #46515b; }
.console-terminal {
  height: 520px;
  padding: 8px 8px 5px 10px;
  background: #171b1f;
}
.console-terminal :deep(.xterm) { height: 100%; user-select: none; }
.console-terminal :deep(.xterm-viewport) {
  overscroll-behavior: contain;
  scrollbar-color: #59656f #171b1f;
  scrollbar-gutter: stable;
  scrollbar-width: auto;
  touch-action: pan-y;
  -webkit-overflow-scrolling: touch;
}
.console-terminal :deep(.xterm-screen) { touch-action: pan-y; }
.console-terminal :deep(.xterm-viewport::-webkit-scrollbar) { width: 14px; }
.console-terminal :deep(.xterm-viewport::-webkit-scrollbar-track) { background: #171b1f; }
.console-terminal :deep(.xterm-viewport::-webkit-scrollbar-thumb) {
  min-height: 36px;
  background: #59656f;
  border: 3px solid #171b1f;
  border-radius: 8px;
}
.console-terminal :deep(.xterm-viewport::-webkit-scrollbar-thumb:hover) { background: #74808a; }
.console-command {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 8px;
  padding: 9px;
  background: #22282d;
  border-top: 1px solid #343b42;
}
.console-command :deep(.el-input__wrapper) {
  background: #171b1f;
  box-shadow: 0 0 0 1px #3b454e inset;
}
.console-command :deep(.el-input__inner) { color: #e1e7ec; font-family: Consolas, monospace; }
@media (max-width: 680px) {
  .console-toolbar { align-items: flex-start; }
  .console-status small { display: none; }
  .console-terminal { height: 430px; padding-inline: 8px; }
  .console-actions :deep(.el-checkbox__label) { display: none; }
}
</style>
