<script setup>
import { nextTick, onBeforeUnmount, ref, watch } from "vue";
import Convert from "ansi-to-html";
import { Eraser, RefreshCw, Send } from "lucide-vue-next";
import { ElMessage } from "element-plus";
import { request } from "../../api";

const props = defineProps({
  nodeId: { type: String, required: true },
  instanceId: { type: String, required: true },
  enabled: { type: Boolean, default: false },
  canCommand: { type: Boolean, default: false },
  running: { type: Boolean, default: false },
});

const outputRef = ref();
const lines = ref([]);
const status = ref("disconnected");
const statusMessage = ref("");
const command = ref("");
const sending = ref(false);
const autoScroll = ref(true);
let socket;
let reconnectTimer;
let generation = 0;
let lastSequence = 0;
let converter = createConverter();

function createConverter() {
  return new Convert({
    fg: "#d8dee9",
    bg: "#171b1f",
    newline: false,
    escapeXML: true,
    stream: true,
  });
}

function websocketURL(value) {
  const target = new URL(value, window.location.href);
  if (target.protocol === "http:") target.protocol = "ws:";
  if (target.protocol === "https:") target.protocol = "wss:";
  return target.toString();
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
  reconnectTimer = window.setTimeout(() => connect(expectedGeneration), 3000);
}

async function connect(expectedGeneration = generation) {
  if (!props.enabled || !props.nodeId || !props.instanceId || expectedGeneration !== generation) return;
  status.value = "connecting";
  statusMessage.value = "";
  try {
    const ticket = await request(
      `/api/v1/instances/${encodeURIComponent(props.instanceId)}/console-ticket?node_id=${encodeURIComponent(props.nodeId)}`,
      { method: "POST", body: "{}" },
    );
    if (expectedGeneration !== generation) return;
    const current = new WebSocket(websocketURL(ticket.websocket_url));
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
        return;
      }
      if (message.type === "proxy.error") {
        status.value = "error";
        statusMessage.value = message.message || "控制台代理连接失败";
        return;
      }
      if (message.type === "console.line") appendLine(message);
    };
    current.onerror = () => {
      status.value = "error";
      statusMessage.value = "控制台连接失败";
    };
    current.onclose = () => {
      if (socket === current) socket = undefined;
      scheduleReconnect(expectedGeneration);
    };
  } catch (error) {
    status.value = "error";
    statusMessage.value = error.message;
    scheduleReconnect(expectedGeneration);
  }
}

function appendLine(line) {
  lastSequence = Math.max(lastSequence, Number(line.sequence) || 0);
  // escapeXML 保证日志正文不会通过 v-html 注入页面。
  lines.value.push({ ...line, html: converter.toHtml(String(line.content ?? "")) });
  if (lines.value.length > 5000) lines.value.splice(0, lines.value.length - 5000);
  if (autoScroll.value) {
    nextTick(() => {
      const element = outputRef.value;
      if (element) element.scrollTop = element.scrollHeight;
    });
  }
}

function clearOutput() {
  lines.value = [];
  converter = createConverter();
}

function reconnect() {
  stopConnection();
  connect(generation);
}

async function sendCommand() {
  const value = command.value.trim();
  if (!value || sending.value || !props.canCommand) return;
  sending.value = true;
  try {
    await request(
      `/api/v1/instances/${encodeURIComponent(props.instanceId)}/command?node_id=${encodeURIComponent(props.nodeId)}`,
      { method: "POST", body: JSON.stringify({ command: value }) },
    );
    command.value = "";
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    sending.value = false;
  }
}

watch(
  () => [props.nodeId, props.instanceId, props.enabled],
  () => {
    stopConnection();
    lines.value = [];
    lastSequence = 0;
    converter = createConverter();
    status.value = props.enabled ? "connecting" : "disconnected";
    if (props.enabled) connect(generation);
  },
  { immediate: true },
);

onBeforeUnmount(stopConnection);
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
          <button class="console-icon-button" type="button" aria-label="清空显示" @click="clearOutput">
            <Eraser :size="15" />
          </button>
        </el-tooltip>
      </div>
    </div>
    <div ref="outputRef" class="console-output" role="log" aria-live="off">
      <div v-if="!lines.length" class="console-empty">暂无控制台输出</div>
      <div v-for="(line, index) in lines" :key="line.session_id + '-' + line.sequence + '-' + index" class="console-line">
        <span class="console-content" v-html="line.html" />
      </div>
    </div>
    <form v-if="canCommand" class="console-command" @submit.prevent="sendCommand">
      <el-input v-model="command" :disabled="!running" maxlength="8192" placeholder="输入控制台命令" />
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
.console-output {
  height: 520px;
  padding: 10px 0;
  overflow: auto;
  color: #d8dee9;
  background: #171b1f;
  font: 12px/1.55 Consolas, "Cascadia Mono", "Courier New", monospace;
}
.console-line {
  min-height: 20px;
  padding: 1px 12px;
}
.console-line:hover { background: #1e2429; }
.console-content {
  display: block;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.console-empty {
  display: grid;
  place-items: center;
  height: 100%;
  color: #68737c;
  font-size: 11px;
}
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
  .console-output { height: 430px; font-size: 11px; }
  .console-line { padding-inline: 8px; }
  .console-actions :deep(.el-checkbox__label) { display: none; }
}
</style>
