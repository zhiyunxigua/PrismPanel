const state = {
  servers: [],
  instances: [],
  selectedServerID: "",
  selectedInstanceID: "",
  consoleInstanceID: "",
  consoleSocket: null,
  activeDeployment: null,
  deploymentTimer: null,
};

const elements = {
  homeView: document.querySelector("#home-view"),
  detailView: document.querySelector("#detail-view"),
  connectionDot: document.querySelector("#connection-dot"),
  connectionText: document.querySelector("#connection-text"),
  notice: document.querySelector("#notice"),
  detailNotice: document.querySelector("#detail-notice"),
  serverSummary: document.querySelector("#server-summary"),
  serverGrid: document.querySelector("#server-grid"),
  emptyState: document.querySelector("#empty-state"),
  detailName: document.querySelector("#detail-name"),
  detailType: document.querySelector("#detail-type"),
  detailID: document.querySelector("#detail-id"),
  detailPath: document.querySelector("#detail-path"),
  detailState: document.querySelector("#detail-state"),
  detailPort: document.querySelector("#detail-port"),
  detailPID: document.querySelector("#detail-pid"),
  detailWorkspace: document.querySelector("#detail-workspace"),
  detailActions: document.querySelectorAll("[data-detail-action]"),
  mirrorToolbar: document.querySelector("#mirror-toolbar"),
  instanceSwitcher: document.querySelector("#instance-switcher"),
  consoleState: document.querySelector("#console-state"),
  consoleOutput: document.querySelector("#console-output"),
  commandForm: document.querySelector("#command-form"),
  commandInput: document.querySelector("#command-input"),
  deploymentPanel: document.querySelector("#deployment-panel"),
  deploymentState: document.querySelector("#deployment-state"),
  deploymentProgress: document.querySelector("#deployment-progress"),
  deploymentLog: document.querySelector("#deployment-log"),
  deploySelected: document.querySelector("#deploy-selected"),
  deployAll: document.querySelector("#deploy-all"),
  cancelDeployment: document.querySelector("#cancel-deployment"),
  forceStopDeployment: document.querySelector("#force-stop-deployment"),
  dialog: document.querySelector("#server-dialog"),
  serverForm: document.querySelector("#server-form"),
  typeInputs: document.querySelectorAll('input[name="type"]'),
  typeSections: document.querySelectorAll("[data-type-section]"),
  instanceCount: document.querySelector('input[name="instance_count"]'),
  portsEditor: document.querySelector("#ports-editor"),
  excludeEditor: document.querySelector("#exclude-editor"),
};

const stateLabels = {
  stopped: "已停止",
  starting: "启动中",
  running: "运行中",
  stopping: "停止中",
  deploying: "部署中",
  failed: "失败",
};

const deploymentLabels = {
  queued: "排队中",
  running: "部署中",
  cancel_requested: "等待取消",
  force_stop_requested: "正在强制结束",
  cancelled: "已取消",
  force_stopped: "已强制结束",
  completed: "已完成",
  completed_with_errors: "部分失败",
  failed: "失败",
};

async function api(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json", ...options.headers },
    ...options,
  });
  const payload = await response.json().catch(() => ({
    success: false,
    error: { code: "INVALID_RESPONSE", message: "服务返回了无效响应" },
  }));
  if (!response.ok || !payload.success) {
    const error = new Error(payload.error?.message || "请求失败");
    error.code = payload.error?.code || "REQUEST_FAILED";
    throw error;
  }
  return payload.data;
}

async function refreshHealth() {
  try {
    const health = await api("/api/v1/health");
    const online = Boolean(health.daemon_connected);
    elements.connectionDot.className = `connection-dot ${online ? "online" : "offline"}`;
    elements.connectionText.textContent = online ? "守护进程已连接" : "守护进程未连接";
  } catch {
    elements.connectionDot.className = "connection-dot offline";
    elements.connectionText.textContent = "面板不可用";
  }
}

async function refreshServers(showFailure = false) {
  try {
    const data = await api("/api/v1/servers");
    state.servers = data.servers || [];
    state.instances = data.instances || [];
    renderHome();
    if (state.selectedServerID) renderDetail();
  } catch (error) {
    if (showFailure) showNotice(`${error.code}: ${error.message}`);
  }
}

function renderHome() {
  elements.serverSummary.textContent = `${state.servers.length} 个服务器组`;
  elements.emptyState.hidden = state.servers.length > 0;
  elements.serverGrid.replaceChildren();
  for (const server of state.servers) {
    const instances = instancesForServer(server.server_id);
    const running = instances.filter((item) => item.state === "running").length;
    const failed = instances.filter((item) => item.state === "failed").length;
    const transitioning = instances.some((item) => ["starting", "stopping", "deploying"].includes(item.state));
    const card = document.createElement("article");
    card.className = `server-card ${failed ? "failed" : transitioning ? "transitioning" : running ? "running" : ""}`;
    const button = document.createElement("button");
    button.type = "button";
    button.className = "server-card-button";
    button.innerHTML = `
      <div class="server-card-header">
        <div><h3>${escapeHTML(server.name)}</h3><span class="server-card-id">${escapeHTML(server.server_id)}</span></div>
        <span class="type-badge">${server.type === "mirror" ? "镜像组" : "普通实例"}</span>
      </div>
      <div class="server-card-stats">
        <div><span>运行</span><strong>${running} / ${instances.length}</strong></div>
        <div><span>异常</span><strong>${failed}</strong></div>
        <div><span>端口</span><strong>${formatServerPorts(server)}</strong></div>
      </div>
      <div class="server-card-footer">
        <span>${aggregateState(instances)}</span>
        <span>${server.type === "mirror" ? `${server.instance_count} 个镜像` : "单实例"}</span>
      </div>`;
    button.addEventListener("click", () => {
      location.hash = `server/${encodeURIComponent(server.server_id)}`;
    });
    card.append(button);
    elements.serverGrid.append(card);
  }
}

function route() {
  const match = location.hash.match(/^#server\/([^/]+)$/);
  if (!match) {
    state.selectedServerID = "";
    state.selectedInstanceID = "";
    closeConsole();
    elements.homeView.hidden = false;
    elements.detailView.hidden = true;
    return;
  }
  state.selectedServerID = decodeURIComponent(match[1]);
  const instances = instancesForServer(state.selectedServerID);
  if (!instances.some((item) => item.instance_id === state.selectedInstanceID)) {
    state.selectedInstanceID = instances[0]?.instance_id || "";
  }
  elements.homeView.hidden = true;
  elements.detailView.hidden = false;
  renderDetail();
}

function renderDetail() {
  const server = selectedServer();
  const instance = selectedInstance();
  if (!server || !instance) {
    location.hash = "";
    return;
  }
  elements.detailName.textContent = server.name;
  elements.detailType.textContent = server.type === "mirror" ? "镜像组" : "普通实例";
  elements.detailID.textContent = server.server_id;
  elements.detailPath.textContent = `${server.name} / ${instance.instance_id}`;
  elements.detailState.textContent = stateLabels[instance.state] || instance.state;
  elements.detailPort.textContent = String(instance.runtime_port ?? instance.configured_port);
  elements.detailPID.textContent = instance.pid || "-";
  elements.detailWorkspace.textContent = instance.workspace;
  renderLifecycleActions(instance);

  const mirror = server.type === "mirror";
  elements.mirrorToolbar.hidden = !mirror;
  if (mirror) renderInstanceSwitcher(server);
  if (state.activeDeployment?.server_id === server.server_id) {
    renderDeployment(state.activeDeployment);
  } else {
    elements.deploymentPanel.hidden = true;
  }
  ensureConsole(instance.instance_id);
}

function renderLifecycleActions(instance) {
  const busy = ["starting", "stopping", "deploying"].includes(instance.state);
  for (const button of elements.detailActions) {
    const action = button.dataset.detailAction;
    button.disabled = busy
      || (action === "start" && instance.state === "running")
      || (["stop", "restart", "kill"].includes(action) && instance.state !== "running");
  }
}

function renderInstanceSwitcher(server) {
  elements.instanceSwitcher.replaceChildren();
  for (const instance of instancesForServer(server.server_id)) {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = `#${instance.slot}`;
    button.className = [
      instance.instance_id === state.selectedInstanceID ? "active" : "",
      instance.state === "running" ? "running" : "",
    ].filter(Boolean).join(" ");
    button.title = `${instance.instance_id} · ${stateLabels[instance.state] || instance.state}`;
    button.addEventListener("click", () => {
      if (state.selectedInstanceID === instance.instance_id) return;
      state.selectedInstanceID = instance.instance_id;
      closeConsole();
      renderDetail();
    });
    elements.instanceSwitcher.append(button);
  }
  const deploying = state.activeDeployment
    && state.activeDeployment.server_id === server.server_id
    && !deploymentFinished(state.activeDeployment.status);
  elements.deploySelected.disabled = deploying;
  elements.deployAll.disabled = deploying;
}

async function performLifecycleAction(action) {
  const instance = selectedInstance();
  if (!instance) return;
  if (action === "kill" && !window.confirm(`确认强制结束 ${instance.instance_id} 的进程树？`)) return;
  try {
    await api(`/api/v1/instances/${encodeURIComponent(instance.instance_id)}/${action}`, {
      method: "POST",
      body: "{}",
    });
    await refreshServers();
  } catch (error) {
    showNotice(`${error.code}: ${error.message}`);
  }
}

function ensureConsole(instanceID) {
  if (state.consoleInstanceID === instanceID && state.consoleSocket?.readyState < WebSocket.CLOSING) {
    setCommandEnabled(selectedInstance()?.state === "running");
    return;
  }
  openConsole(instanceID);
}

async function openConsole(instanceID) {
  closeConsole();
  state.consoleInstanceID = instanceID;
  elements.consoleOutput.textContent = "";
  elements.consoleState.textContent = "正在连接";
  setCommandEnabled(selectedInstance()?.state === "running");
  try {
    const ticket = await api(`/api/v1/instances/${encodeURIComponent(instanceID)}/console-ticket`, {
      method: "POST",
      body: "{}",
    });
    let endpoint = ticket.websocket_url;
    if (endpoint.startsWith("/")) {
      const scheme = location.protocol === "https:" ? "wss:" : "ws:";
      endpoint = `${scheme}//${location.host}${endpoint}`;
    }
    const socket = new WebSocket(endpoint);
    state.consoleSocket = socket;
    socket.addEventListener("open", () => {
      socket.send(JSON.stringify({
        type: "auth", ticket: ticket.ticket, instance_id: instanceID, after_sequence: 0,
      }));
    });
    socket.addEventListener("message", (event) => {
      let message;
      try {
        message = JSON.parse(event.data);
      } catch {
        return;
      }
      if (message.type === "auth.result") {
        elements.consoleState.textContent = message.success ? "实时日志已连接" : "日志认证失败";
        if (!message.success) showNotice(message.error?.message || "日志认证失败");
      } else if (message.type === "console.line") {
        appendConsoleLine(message);
      } else if (message.type === "proxy.error") {
        showNotice(message.message);
      }
    });
    socket.addEventListener("close", () => {
      if (state.consoleSocket === socket) {
        elements.consoleState.textContent = "日志连接已断开";
        state.consoleSocket = null;
      }
    });
    socket.addEventListener("error", () => {
      elements.consoleState.textContent = "日志连接错误";
    });
  } catch (error) {
    elements.consoleState.textContent = "无法连接日志";
    showNotice(`${error.code}: ${error.message}`);
  }
}

function closeConsole() {
  if (state.consoleSocket) state.consoleSocket.close();
  state.consoleSocket = null;
  state.consoleInstanceID = "";
  setCommandEnabled(false);
}

function appendConsoleLine(line) {
  const timestamp = new Date(line.timestamp).toLocaleTimeString();
  elements.consoleOutput.textContent += `[${timestamp}] [${line.stream}] ${line.content}\n`;
  if (elements.consoleOutput.textContent.length > 500000) {
    elements.consoleOutput.textContent = elements.consoleOutput.textContent.slice(-400000);
  }
  elements.consoleOutput.scrollTop = elements.consoleOutput.scrollHeight;
}

function setCommandEnabled(enabled) {
  elements.commandInput.disabled = !enabled;
  elements.commandForm.querySelector("button").disabled = !enabled;
}

async function startDeployment(all) {
  const server = selectedServer();
  const instance = selectedInstance();
  if (!server || server.type !== "mirror" || !instance) return;
  try {
    const task = await api(`/api/v1/servers/${encodeURIComponent(server.server_id)}/deploy`, {
      method: "POST",
      body: JSON.stringify({ targets: all ? [] : [instance.slot] }),
    });
    state.activeDeployment = task;
    renderDeployment(task);
    scheduleDeploymentPoll();
    await refreshServers();
  } catch (error) {
    showNotice(`${error.code}: ${error.message}`);
  }
}

function scheduleDeploymentPoll() {
  window.clearTimeout(state.deploymentTimer);
  state.deploymentTimer = window.setTimeout(pollDeployment, 800);
}

async function pollDeployment() {
  if (!state.activeDeployment) return;
  try {
    const task = await api(`/api/v1/deployments/${encodeURIComponent(state.activeDeployment.task_id)}`);
    state.activeDeployment = task;
    renderDeployment(task);
    await refreshServers();
    if (!deploymentFinished(task.status)) scheduleDeploymentPoll();
  } catch (error) {
    showNotice(`${error.code}: ${error.message}`);
  }
}

function renderDeployment(task) {
  elements.deploymentPanel.hidden = state.selectedServerID !== task.server_id;
  elements.deploymentState.textContent = deploymentLabels[task.status] || task.status;
  elements.deploymentProgress.textContent = `${task.completed} / ${task.targets.length}`;
  elements.deploymentLog.replaceChildren();
  for (const log of task.logs || []) {
    const row = document.createElement("div");
    row.className = `deployment-log-line ${log.level}`;
    row.innerHTML = `<span>${new Date(log.timestamp).toLocaleTimeString()}</span><span>${escapeHTML(log.stage)}</span><span>${escapeHTML(log.message)}</span>`;
    elements.deploymentLog.append(row);
  }
  elements.deploymentLog.scrollTop = elements.deploymentLog.scrollHeight;
  const active = !deploymentFinished(task.status);
  elements.cancelDeployment.hidden = !active;
  elements.forceStopDeployment.hidden = !active;
  elements.deploySelected.disabled = active;
  elements.deployAll.disabled = active;
}

async function stopDeployment(force) {
  if (!state.activeDeployment) return;
  if (force && !window.confirm("强制结束后不会恢复实例运行状态，确认继续？")) return;
  const action = force ? "force-stop" : "cancel";
  try {
    const task = await api(`/api/v1/deployments/${encodeURIComponent(state.activeDeployment.task_id)}/${action}`, {
      method: "POST", body: "{}",
    });
    state.activeDeployment = task;
    renderDeployment(task);
    scheduleDeploymentPoll();
  } catch (error) {
    showNotice(`${error.code}: ${error.message}`);
  }
}

function deploymentFinished(status) {
  return ["cancelled", "force_stopped", "completed", "completed_with_errors", "failed"].includes(status);
}

function selectedServer() {
  return state.servers.find((server) => server.server_id === state.selectedServerID);
}

function selectedInstance() {
  return state.instances.find((instance) => instance.instance_id === state.selectedInstanceID);
}

function instancesForServer(serverID) {
  return state.instances
    .filter((instance) => instance.server_id === serverID)
    .sort((left, right) => (left.slot || 0) - (right.slot || 0));
}

function aggregateState(instances) {
  if (!instances.length) return "无实例";
  if (instances.some((item) => item.state === "failed")) return "存在异常";
  if (instances.some((item) => ["starting", "stopping", "deploying"].includes(item.state))) return "状态切换中";
  const running = instances.filter((item) => item.state === "running").length;
  if (running === instances.length) return "全部运行";
  if (running > 0) return "部分运行";
  return "全部停止";
}

function formatServerPorts(server) {
  if (server.type !== "mirror") return String(server.port);
  if (!server.ports?.length) return "-";
  return server.ports.length === 1 ? String(server.ports[0]) : `${server.ports[0]}–${server.ports[server.ports.length - 1]}`;
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function showNotice(message) {
  const target = state.selectedServerID ? elements.detailNotice : elements.notice;
  target.textContent = message;
  target.hidden = false;
  window.clearTimeout(showNotice.timer);
  showNotice.timer = window.setTimeout(() => {
    target.hidden = true;
  }, 6000);
}

function selectedServerType() {
  return document.querySelector('input[name="type"]:checked').value;
}

function updateServerType() {
  const selected = selectedServerType();
  for (const section of elements.typeSections) {
    const active = section.dataset.typeSection === selected;
    section.hidden = !active;
    for (const control of section.querySelectorAll("input, select, textarea, button")) {
      control.disabled = !active;
    }
  }
  if (selected === "mirror") syncPortInputs();
}

function syncPortInputs() {
  const count = Math.max(1, Math.min(128, Number(elements.instanceCount.value) || 1));
  const previous = [...elements.portsEditor.querySelectorAll("input")].map((input) => input.value);
  elements.portsEditor.replaceChildren();
  for (let index = 0; index < count; index += 1) {
    const label = document.createElement("label");
    label.className = "port-field";
    const number = document.createElement("span");
    number.textContent = `#${index + 1}`;
    const input = document.createElement("input");
    input.type = "number";
    input.min = "1";
    input.max = "65535";
    input.required = true;
    input.value = previous[index] || String(25571 + index);
    label.append(number, input);
    elements.portsEditor.append(label);
  }
}

function addExcludeRow(type = "directory", path = "") {
  const row = document.createElement("div");
  row.className = "exclude-row";
  const select = document.createElement("select");
  select.innerHTML = '<option value="directory">文件夹</option><option value="file">文件</option>';
  select.value = type;
  const input = document.createElement("input");
  input.type = "text";
  input.placeholder = "world";
  input.value = path;
  const remove = document.createElement("button");
  remove.className = "icon-button";
  remove.type = "button";
  remove.setAttribute("aria-label", "删除排除项");
  remove.textContent = "×";
  remove.addEventListener("click", () => row.remove());
  row.append(select, input, remove);
  elements.excludeEditor.append(row);
}

async function createServer(form) {
  const values = new FormData(form);
  const type = values.get("type");
  const config = {
    schema_version: 1,
    type,
    server_id: values.get("server_id"),
    name: values.get("name"),
    process: {
      start_command: values.get("start_command").trim(),
      stop_command: values.get("stop_command").trim(),
      stop_timeout_seconds: Number(values.get("stop_timeout")),
      auto_start: false,
      auto_restart: values.get("auto_restart") === "on",
    },
    console: { encoding: "utf-8" },
  };
  if (type === "standalone") {
    config.workspace = values.get("workspace");
    config.port = Number(values.get("port"));
  } else {
    config.root_path = values.get("root_path");
    config.image_directory = values.get("image_directory");
    config.instance_count = Number(values.get("instance_count"));
    config.ports = [...elements.portsEditor.querySelectorAll("input")].map((input) => Number(input.value));
    config.exclude = [...elements.excludeEditor.querySelectorAll(".exclude-row")]
      .filter((row) => row.querySelector("input").value.trim())
      .map((row) => ({
        type: row.querySelector("select").value,
        path: row.querySelector("input").value.trim(),
      }));
  }
  try {
    await api("/api/v1/servers", { method: "POST", body: JSON.stringify(config) });
    elements.dialog.close();
    form.reset();
    elements.excludeEditor.replaceChildren();
    updateServerType();
    await refreshServers();
  } catch (error) {
    showNotice(`${error.code}: ${error.message}`);
  }
}

for (const button of elements.detailActions) {
  button.addEventListener("click", () => performLifecycleAction(button.dataset.detailAction));
}

elements.commandForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const instance = selectedInstance();
  const command = elements.commandInput.value;
  if (!instance || !command) return;
  elements.commandInput.value = "";
  try {
    await api(`/api/v1/instances/${encodeURIComponent(instance.instance_id)}/command`, {
      method: "POST",
      body: JSON.stringify({ command }),
    });
  } catch (error) {
    showNotice(`${error.code}: ${error.message}`);
  }
});

elements.deploySelected.addEventListener("click", () => startDeployment(false));
elements.deployAll.addEventListener("click", () => startDeployment(true));
elements.cancelDeployment.addEventListener("click", () => stopDeployment(false));
elements.forceStopDeployment.addEventListener("click", () => stopDeployment(true));
elements.serverForm.addEventListener("submit", (event) => {
  event.preventDefault();
  createServer(event.currentTarget);
});
for (const input of elements.typeInputs) {
  input.addEventListener("change", updateServerType);
}
elements.instanceCount.addEventListener("input", syncPortInputs);

document.querySelector("#add-exclude").addEventListener("click", () => addExcludeRow());
document.querySelector("#create-button").addEventListener("click", () => {
  updateServerType();
  elements.dialog.showModal();
});
document.querySelector("#close-dialog").addEventListener("click", () => elements.dialog.close());
document.querySelector("#cancel-dialog").addEventListener("click", () => elements.dialog.close());
document.querySelector("#refresh-button").addEventListener("click", () => refreshServers(true));
document.querySelector("#clear-console").addEventListener("click", () => {
  elements.consoleOutput.textContent = "";
});
document.querySelector("#home-button").addEventListener("click", () => {
  location.hash = "";
});
document.querySelector("#back-button").addEventListener("click", () => {
  location.hash = "";
});
window.addEventListener("hashchange", route);

async function bootstrap() {
  updateServerType();
  await Promise.all([refreshHealth(), refreshServers(true)]);
  route();
  window.setInterval(refreshHealth, 5000);
  window.setInterval(() => refreshServers(false), 2500);
}

bootstrap();
