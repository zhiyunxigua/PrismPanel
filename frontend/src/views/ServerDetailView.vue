<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { onBeforeRouteLeave, useRoute, useRouter } from "vue-router";
import {
  Activity, ArrowLeft, ArrowRightLeft, Cpu, Edit3, FileCode2, MemoryStick, OctagonX, Play,
  PlugZap, Puzzle, RefreshCw, RotateCw, Server, Square, Terminal, Trash2, Upload, Users,
} from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { request, requestWithProgress } from "../api";
import { hasPermission } from "../session";
import { showUploadResult } from "../uploadResult";
import {
  SERVER_LIST_RETRIES,
  SERVER_LIST_RETRY_DELAY_MS,
  resolveServerList,
  sleep,
} from "./server-detail-resolve";
import ConsoleOutput from "../components/servers/ConsoleOutput.vue";
import MetricLineChart from "../components/metrics/MetricLineChart.vue";
import ServerEditorDialog from "../components/servers/ServerEditorDialog.vue";
import FileManager from "../components/files/FileManager.vue";
import TargetSelectionTree from "../components/TargetSelectionTree.vue";

const route = useRoute();
const router = useRouter();
const loading = ref(false);
const loadError = ref("");
const submitting = ref(false);
const server = ref(null);
const instances = ref([]);
const activeTab = ref(hasPermission("console.read") ? "console" : "overview");
const selectedInstanceId = ref("");
const pluginInstanceId = ref("");
const pluginSearch = ref("");
const pluginData = ref({ items: [], warnings: [], pending_restart: false });
const pluginLoading = ref(false);
const pluginActionLoading = ref("");
const pluginUploadInput = ref(null);
const pluginUploadState = ref({
  active: false, completed: 0, total: 0, name: "",
  installed: 0, replaced: 0, skipped: 0, failed: 0, failures: [],
  progress: { loaded: 0, total: 0, percent: 0 },
});
const pluginDropActive = ref(false);
const pluginConflictOpen = ref(false);
const pluginConflict = ref({ fileName: "", incoming: {}, existing: {} });
let pluginConflictResolver = null;
const uninstallOpen = ref(false);
const uninstallTarget = ref(null);
const uninstallForm = ref({ deleteConfig: false, configDirectory: "" });
const metricSeries = ref([]);
const healthInstanceId = ref("");
const editorOpen = ref(false);
const allNodeContents = ref([]);
const proxyRules = ref([]);
const proxyStatus = ref(null);
const proxySelections = ref([]);
const proxyConfigLoading = ref(false);
const transferOpen = ref(false);
const transferSubmitting = ref(false);
const transferForm = ref({ player: null, targetServerId: "" });
const deploymentOpen = ref(false);
const deploymentMode = ref("mirror_deploy");
const deploymentTargets = ref([]);
const deploymentTask = ref(null);
const deploymentSubmitting = ref(false);
const deploymentRefreshing = ref(false);
const actionLoading = reactive({});
// 子服操作中文标签（invoke/confirmAction 共用，必须为模块级——放函数内会导致 invoke 中未定义）。
const instanceActionLabels = { start: "启动", stop: "关闭", restart: "重启", kill: "强制关闭" };
let refreshTimer;
let deploymentTimer;

const nodeId = computed(() => String(route.params.nodeId));
const serverId = computed(() => String(route.params.serverId));
const nodeName = computed(() => String(route.query.node_name || nodeId.value));
const selectedInstance = computed(() => instances.value.find((item) => item.instance_id === selectedInstanceId.value));
const pluginInstance = computed(() => instances.value.find((item) => item.instance_id === pluginInstanceId.value));
const canConfigure = computed(() => hasPermission("server.configure"));
const canDeploy = computed(() => hasPermission("server.deploy"));
const canDelete = computed(() => hasPermission("server.delete"));
const canReadConsole = computed(() => hasPermission("console.read"));
const canReadFiles = computed(() => hasPermission("file.read"));
const canCommand = computed(() => hasPermission("console.command"));
const canViewPlugins = computed(() => hasPermission("plugin.view"));
const canDeployPlugins = computed(() => hasPermission("plugin.deploy"));
const canRemovePlugins = computed(() => hasPermission("plugin.remove"));
const canRestartUploadedPlugin = computed(() => (
  hasPermission("instance.restart")
  && pluginInstance.value?.state === "running"
  && !pluginInstance.value?.deployment_locked
));
const pluginConflictRestartNote = computed(() => {
  if (pluginInstance.value?.state === "stopped" || pluginInstance.value?.state === "failed") {
    return "新版本将在下次启动时生效。";
  }
  if (!hasPermission("instance.restart")) {
    return "替换后需要由有权限的管理员重启当前子服。";
  }
  if (!canRestartUploadedPlugin.value) {
    return "当前子服状态不允许立即重启，替换后将在下次启动时生效。";
  }
  return "替换后需要重启当前子服才能加载新版本。";
});
const canViewPlayers = computed(() => hasPermission("player.view"));
const canTransferPlayers = computed(() => hasPermission("player.transfer"));
const isProxyServer = computed(() => ["velocity", "bungee"].includes(server.value?.platform));
const canViewTasks = computed(() => hasPermission("task.view"));
const canCancelTasks = computed(() => hasPermission("task.cancel"));
const canReadDeployment = computed(() => canViewTasks.value || canDeploy.value);
const runningCount = computed(() => instances.value.filter((item) => item.state === "running").length);
const allStopped = computed(() => instances.value.every((item) => item.state === "stopped" && !item.deployment_locked));
const groupBusy = computed(() => instances.value.some((item) => (
  item.state === "starting" || item.state === "stopping" || item.state === "deploying"
)));
const deploymentGroupLocked = computed(() => instances.value.some((item) => (
  item.deployment_locked || item.state === "deploying"
)));
const healthSeries = computed(() => metricSeries.value.find((item) => item.instance_id === healthInstanceId.value));
const cpuHistory = computed(() => metricPoints("cpu_percent"));
const memoryHistory = computed(() => metricPoints("memory_bytes", (value) => value / 1024 / 1024 / 1024));
const playerHistory = computed(() => metricPoints("online_players"));
const tpsHistory = computed(() => metricPoints("tps"));
const deploymentActive = computed(() => deploymentTask.value && ![
  "cancelled", "force_stopped", "completed", "completed_with_errors", "failed",
].includes(deploymentTask.value.status));
const pluginConfigSyncMode = computed(() => deploymentMode.value === "plugin_config_sync");
const deploymentProgress = computed(() => {
  const total = deploymentTask.value?.targets?.length || 0;
  if (!total) return 0;
  return Math.min(100, Math.round((Number(deploymentTask.value.completed) || 0) / total * 100));
});
const syncDirectoriesText = computed(() => {
  const directories = server.value?.config_sync_directories?.length
    ? server.value.config_sync_directories
    : ["plugins"];
  return directories.join(", ");
});
const deploymentCopyProgress = computed(() => {
  const totalBytes = Number(deploymentTask.value?.copy_bytes_total) || 0;
  const doneBytes = Number(deploymentTask.value?.copy_bytes_done) || 0;
  const totalFiles = Number(deploymentTask.value?.copy_files_total) || 0;
  const doneFiles = Number(deploymentTask.value?.copy_files_done) || 0;
  if (totalBytes > 0) return Math.min(100, Math.round(doneBytes / totalBytes * 100));
  if (totalFiles > 0) return Math.min(100, Math.round(doneFiles / totalFiles * 100));
  return deploymentTask.value?.copy_stage === "finalizing" ? 100 : 0;
});
const pluginRestartPending = computed(() => (
  (pluginData.value.instance_id === pluginInstanceId.value && pluginData.value.pending_restart)
  || pluginInstance.value?.plugin_pending_restart
));
const pluginRestartRequired = computed(() => (
  pluginRestartPending.value && pluginInstance.value?.state === "running"
));
const pluginPendingTitle = computed(() => {
  if (pluginRestartRequired.value) {
    return "插件文件已变更，需要重启当前子服才能应用";
  }
  if (["stopped", "failed"].includes(pluginInstance.value?.state)) {
    return "插件文件已变更，将在下次启动当前子服时应用";
  }
  return "插件文件已变更，将在当前状态切换完成后应用";
});
const deploymentStatusLabels = {
  queued: "等待执行", running: "部署中", cancel_requested: "正在取消",
  force_stop_requested: "正在强制结束", cancelled: "已取消", force_stopped: "已强制结束",
  completed: "部署完成", completed_with_errors: "部分失败", failed: "部署失败",
};
const pluginConfigSyncStatusLabels = {
  queued: "等待同步", running: "同步中", cancel_requested: "正在取消",
  force_stop_requested: "正在强制结束", cancelled: "已取消", force_stopped: "已强制结束",
  completed: "同步完成", completed_with_errors: "同步完成，部分失败", failed: "同步失败",
};
const deploymentStatusText = computed(() => {
  const status = deploymentTask.value?.status;
  if (!status) return "";
  return (pluginConfigSyncMode.value ? pluginConfigSyncStatusLabels : deploymentStatusLabels)[status] || status;
});
const deploymentCopyStageLabels = {
  scanning_plugin_config: "扫描插件配置",
  copying_plugin_config: "复制插件配置",
  scanning_config: "扫描配置目录",
  copying_config: "复制配置目录",
  preparing: "准备目标",
  scanning_image: "扫描镜像文件",
  copying_image: "复制镜像文件",
  scanning_excluded: "扫描排除项",
  restoring_excluded: "恢复排除项",
  finalizing: "写入配置并切换目录",
};
const players = computed(() => instances.value.flatMap((instance) => (
  Array.isArray(instance.players)
    ? instance.players.map((player) => ({ ...player, instance_name: instance.name, instance_id: instance.instance_id }))
    : []
)));
const backendOptions = computed(() => {
  const servers = new Map();
  for (const content of allNodeContents.value) {
    for (const item of content.servers || []) {
      servers.set(content.node.id + ":" + item.server_id, item);
    }
  }
  return allNodeContents.value.flatMap((content) => (content.instances || [])
    .filter((instance) => {
      const target = servers.get(content.node.id + ":" + instance.server_id);
      return target && !["velocity", "bungee"].includes(target.platform);
    })
    .map((instance) => ({
      value: instance.instance_id,
      label: instance.name || instance.instance_id,
      nodeName: content.node.name,
    })));
});
const filteredPlugins = computed(() => {
  const source = Array.isArray(pluginData.value.items) ? pluginData.value.items : [];
  const keyword = pluginSearch.value.trim().toLowerCase();
  return keyword
    ? source.filter((item) => [item.name, item.version].join(" ").toLowerCase().includes(keyword))
    : source;
});
const pluginStatusLabels = {
  loaded: "已装载",
  file_enabled: "文件已启用",
  file_disabled: "文件已禁用",
  not_loaded: "未装载",
  disabled: "已禁用",
  runtime_only: "内置插件",
  disabled_pending_restart: "禁用待重启",
  install_pending_restart: "安装待重启",
  update_pending_restart: "更新待重启",
  uninstall_pending_restart: "卸载待重启",
  conflict: "同名冲突",
};
const stateLabels = {
  stopped: "已停止", starting: "启动中", running: "运行中",
  stopping: "停止中", deploying: "部署中", deployment_pending: "等待部署", failed: "异常",
};

function displayState(instance) {
  if (instance?.deployment_locked && instance.state !== "deploying") return "deployment_pending";
  return instance?.state;
}

function metricPoints(key, transform = (value) => value) {
  return (healthSeries.value?.points || []).map((point) => ({
    sampled_at: point.sampled_at,
    value: point[key] === null || point[key] === undefined ? null : transform(Number(point[key])),
  }));
}

async function loadMetrics() {
  try {
    const path = "/api/v1/servers/" + encodeURIComponent(serverId.value) +
      "/metrics?node_id=" + encodeURIComponent(nodeId.value);
    const data = await request(path);
    metricSeries.value = data.instances || [];
    if (!metricSeries.value.some((item) => item.instance_id === healthInstanceId.value)) {
      healthInstanceId.value = metricSeries.value[0]?.instance_id || instances.value[0]?.instance_id || "";
    }
  } catch {
    // Keep the last complete history during a transient node timeout.
  }
}

async function loadSyncConfiguration() {
  if (!server.value || !canConfigure.value) return;
  proxyConfigLoading.value = true;
  try {
    const catalog = await request("/api/v1/servers");
    allNodeContents.value = catalog.nodes || [];
    if (isProxyServer.value) {
      const query = "?node_id=" + encodeURIComponent(nodeId.value)
        + "&server_id=" + encodeURIComponent(serverId.value);
      const data = await request("/api/v1/proxy-sync-rules" + query);
      proxyRules.value = data.rules || [];
      proxyStatus.value = data.status || null;
    } else {
      const query = "?target_node_id=" + encodeURIComponent(nodeId.value)
        + "&target_server_id=" + encodeURIComponent(serverId.value);
      const data = await request("/api/v1/proxy-sync-rules" + query);
      proxySelections.value = data.proxies || [];
    }
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    proxyConfigLoading.value = false;
  }
}

async function saveProxyRules() {
  proxyConfigLoading.value = true;
  try {
    const query = "?node_id=" + encodeURIComponent(nodeId.value)
      + "&server_id=" + encodeURIComponent(serverId.value);
    const data = await request("/api/v1/proxy-sync-rules" + query, {
      method: "PUT",
      body: JSON.stringify({ rules: proxyRules.value }),
    });
    proxyStatus.value = data.status;
    ElMessage.success("代理服务器列表已同步");
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    proxyConfigLoading.value = false;
  }
}

async function saveProxySelections() {
  proxyConfigLoading.value = true;
  try {
    const query = "?target_node_id=" + encodeURIComponent(nodeId.value)
      + "&target_server_id=" + encodeURIComponent(serverId.value);
    await request("/api/v1/proxy-sync-rules" + query, {
      method: "PUT",
      body: JSON.stringify({
        proxies: proxySelections.value.map((item) => ({
          node_id: item.node_id,
          server_id: item.server_id,
          enabled: item.selected,
        })),
      }),
    });
    ElMessage.success("代理同步配置已保存");
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    proxyConfigLoading.value = false;
  }
}

function openTransfer(player) {
  transferForm.value = { player, targetServerId: player.server_id || backendOptions.value[0]?.value || "" };
  transferOpen.value = true;
}

async function transferPlayer() {
  if (!transferForm.value.player || !transferForm.value.targetServerId) {
    ElMessage.warning("请选择目标服务器");
    return;
  }
  transferSubmitting.value = true;
  try {
    await request("/api/v1/players/transfer", {
      method: "POST",
      body: JSON.stringify({
        node_id: nodeId.value,
        instance_id: transferForm.value.player.instance_id,
        player_uuid: transferForm.value.player.uuid,
        target_server_id: transferForm.value.targetServerId,
      }),
    });
    transferOpen.value = false;
    ElMessage.success("玩家转移请求已完成");
    await load(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    transferSubmitting.value = false;
  }
}

async function loadPlugins(silent = false) {
  if (!pluginInstanceId.value || !canViewPlugins.value) return;
  const instanceID = pluginInstanceId.value;
  if (!silent) pluginLoading.value = true;
  try {
    const path = "/api/v1/instances/" + encodeURIComponent(instanceID) +
      "/plugins?node_id=" + encodeURIComponent(nodeId.value);
    const data = await request(path);
    if (pluginInstanceId.value !== instanceID) return;
    pluginData.value = data;
    if (!silent && pluginData.value.warnings?.length) {
      ElMessage.warning(pluginData.value.warnings.join("；"));
    }
  } catch (error) {
    if (!silent && pluginInstanceId.value === instanceID) ElMessage.error(error.message);
  } finally {
    if (!silent && pluginInstanceId.value === instanceID) pluginLoading.value = false;
  }
}

function choosePluginFiles() {
  if (canDeployPlugins.value && pluginInstanceId.value && !pluginUploadState.value.active) {
    pluginUploadInput.value?.click();
  }
}

async function handlePluginFileInput(event) {
  const files = Array.from(event.target.files || []);
  event.target.value = "";
  await uploadPluginFiles(files);
}

function handlePluginDragOver(event) {
  if (!canDeployPlugins.value || !pluginInstanceId.value || pluginUploadState.value.active) return;
  if (!Array.from(event.dataTransfer?.types || []).includes("Files")) return;
  event.preventDefault();
  event.dataTransfer.dropEffect = "copy";
  pluginDropActive.value = true;
}

function handlePluginDragLeave(event) {
  if (!event.currentTarget.contains(event.relatedTarget)) pluginDropActive.value = false;
}

async function handlePluginDrop(event) {
  if (!canDeployPlugins.value || !pluginInstanceId.value || pluginUploadState.value.active) return;
  event.preventDefault();
  pluginDropActive.value = false;
  const dropped = Array.from(event.dataTransfer?.files || []);
  const files = dropped.filter((file) => file.name.toLowerCase().endsWith(".jar"));
  if (files.length !== dropped.length) ElMessage.warning("已忽略 " + (dropped.length - files.length) + " 个非 JAR 文件");
  await uploadPluginFiles(files);
}

async function uploadInstancePluginFile(file, overwrite, instanceID, onProgress) {
  const query = "?node_id=" + encodeURIComponent(nodeId.value)
    + "&filename=" + encodeURIComponent(file.name)
    + "&overwrite=" + String(overwrite);
  const options = {
    method: "POST",
    headers: { "Content-Type": file.type || "application/java-archive" },
    body: file,
  };
  if (onProgress) return requestWithProgress(
    "/api/v1/instances/" + encodeURIComponent(instanceID) + "/plugins" + query,
    options,
    onProgress,
  );
  return request("/api/v1/instances/" + encodeURIComponent(instanceID) + "/plugins" + query, options);
}

function askPluginConflict(file, data) {
  pluginConflict.value = {
    fileName: file.name,
    incoming: { name: data?.plugin_name || file.name, version: data?.version || "未知" },
    existing: { file: data?.existing_file || "未知文件", version: data?.existing_version || "未知" },
  };
  pluginConflictOpen.value = true;
  return new Promise((resolve) => {
    pluginConflictResolver = resolve;
  });
}

function decidePluginConflict(action) {
  pluginConflictOpen.value = false;
  const resolve = pluginConflictResolver;
  pluginConflictResolver = null;
  resolve?.(action);
}

async function uploadPluginFiles(files) {
  if (!files.length || pluginUploadState.value.active || !pluginInstanceId.value) return;
  const instanceID = pluginInstanceId.value;
  let restartRequested = false;
  pluginUploadState.value = {
    active: true, completed: 0, total: files.length, name: files[0].name,
    installed: 0, replaced: 0, skipped: 0, failed: 0, failures: [],
    progress: { loaded: 0, total: 0, percent: 0 },
  };
  for (const file of files) {
    pluginUploadState.value.name = file.name;
    pluginUploadState.value.progress = { loaded: 0, total: file.size || 0, percent: 0 };
    const onProgress = (event) => {
      pluginUploadState.value.progress = {
        loaded: event.loaded,
        total: event.total,
        percent: event.total ? Math.round((event.loaded / event.total) * 100) : 0,
      };
    };
    try {
      let result;
      try {
        result = await uploadInstancePluginFile(file, false, instanceID, onProgress);
      } catch (error) {
        if (error.code !== "PLUGIN_EXISTS") throw error;
        const action = await askPluginConflict(file, error.data);
        if (action === "skip") {
          pluginUploadState.value.skipped += 1;
          continue;
        }
        result = await uploadInstancePluginFile(file, true, instanceID, onProgress);
        restartRequested = restartRequested || action === "replace-restart";
      }
      if (result.replaced) pluginUploadState.value.replaced += 1;
      else pluginUploadState.value.installed += 1;
    } catch (error) {
      pluginUploadState.value.failed += 1;
      pluginUploadState.value.failures.push({ name: file.name, error: error.message || "上传失败" });
    } finally {
      pluginUploadState.value.completed += 1;
    }
  }
  pluginUploadState.value.active = false;
  await Promise.all([loadPlugins(true), load(true)]);
  showPluginUploadSummary();
  if (restartRequested) await restartAfterPluginUpload(instanceID);
}

function showPluginUploadSummary() {
  const state = pluginUploadState.value;
  const parts = [];
  if (state.installed) parts.push("已安装 " + state.installed + " 个插件");
  if (state.replaced) parts.push("已替换 " + state.replaced + " 个插件");
  if (state.skipped) parts.push("跳过 " + state.skipped + " 个插件");
  showUploadResult(ElMessage, {
    parts,
    successEmpty: "未上传插件",
    failed: state.failed || 0,
    failures: state.failures || [],
    succeeded: (state.installed || 0) + (state.replaced || 0),
    noun: "插件",
  });
}

async function restartAfterPluginUpload(instanceID) {
  try {
    await request(
      "/api/v1/instances/" + encodeURIComponent(instanceID)
        + "/restart?node_id=" + encodeURIComponent(nodeId.value),
      { method: "POST", body: "{}" },
    );
    ElMessage.success("插件已替换，子服正在重启");
    await Promise.all([load(true), loadPlugins(true)]);
  } catch (error) {
    ElMessage.error("插件已替换，但子服重启失败。新版本将在下次成功启动后生效。");
  }
}

async function setPluginEnabled(plugin, enabled) {
  if (!canDeployPlugins.value || pluginActionLoading.value) return;
  pluginActionLoading.value = plugin.name;
  try {
    const action = enabled ? "enable" : "disable";
    const path = "/api/v1/servers/" + encodeURIComponent(serverId.value) +
      "/plugins/" + action + "?node_id=" + encodeURIComponent(nodeId.value);
    const result = await request(path, {
      method: "POST", body: JSON.stringify({ plugin_name: plugin.name }),
    });
    ElMessage.success(result.pending_restart ? "插件状态已更改，重启后生效" : "插件状态已更改");
    await Promise.all([loadPlugins(true), load(true)]);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    pluginActionLoading.value = "";
  }
}

function openUninstall(plugin) {
  uninstallTarget.value = plugin;
  uninstallForm.value = { deleteConfig: false, configDirectory: plugin.name || "" };
  uninstallOpen.value = true;
}

async function uninstallPlugin() {
  if (!uninstallTarget.value || pluginActionLoading.value) return;
  pluginActionLoading.value = uninstallTarget.value.name;
  try {
    const path = "/api/v1/servers/" + encodeURIComponent(serverId.value) +
      "/plugins/uninstall?node_id=" + encodeURIComponent(nodeId.value);
    const result = await request(path, {
      method: "POST",
      body: JSON.stringify({
        plugin_name: uninstallTarget.value.name,
        delete_config: uninstallForm.value.deleteConfig,
        config_directory: uninstallForm.value.deleteConfig ? uninstallForm.value.configDirectory.trim() : "",
      }),
    });
    uninstallOpen.value = false;
    ElMessage.success(result.pending_restart ? "插件已卸载，重启后完全生效" : "插件已卸载");
    await Promise.all([loadPlugins(true), load(true)]);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    pluginActionLoading.value = "";
  }
}

function beforeWindowUnload(event) {
  if (!pluginRestartRequired.value) return;
  event.preventDefault();
  event.returnValue = "";
}

async function load(silent = false) {
  if (!silent) loading.value = true;
  try {
    // 目标缺失/列表为空时短暂重试，避免节点刚重连、daemon 忙碌或
    // 面板返回空 payload 时把「暂时读不到」误判为「服务器不存在」。
    let outcome = null;
    for (let attempt = 0; attempt <= SERVER_LIST_RETRIES; attempt += 1) {
      if (attempt > 0) await sleep(SERVER_LIST_RETRY_DELAY_MS);
      const data = await request(`/api/v1/servers?node_id=${encodeURIComponent(nodeId.value)}`);
      outcome = resolveServerList(data, serverId.value);
      if (outcome.status === "ok" || outcome.status === "node_error" || outcome.status === "missing") {
        break;
      }
      // status === "empty"：列表为空且无节点错误，继续重试。
    }
    if (outcome.status === "node_error") {
      // 节点离线/接口异常：显示节点错误并允许重试，不跳转。
      loadError.value = outcome.message;
      if (!silent) ElMessage.error(outcome.message);
      return;
    }
    if (outcome.status === "empty") {
      // 列表为空但无错误：节点可能刚重连/未就绪，提示刷新重试而不是踢回列表。
      loadError.value = "节点服务器列表暂时为空，节点可能正在重连，请点击刷新重试";
      if (!silent) ElMessage.warning(loadError.value);
      return;
    }
    if (outcome.status === "missing") {
      // 服务器确实不存在（已被删除或路由参数错误）：允许提示并跳回列表。
      if (!silent) ElMessage.warning("服务器不存在，可能已被删除");
      await router.replace({ name: "servers" });
      return;
    }
    loadError.value = "";
    server.value = outcome.server;
    instances.value = outcome.instances;
    if (!instances.value.some((item) => item.instance_id === selectedInstanceId.value)) {
      selectedInstanceId.value = instances.value[0]?.instance_id || "";
    }
    if (!instances.value.some((item) => item.instance_id === pluginInstanceId.value)) {
      pluginInstanceId.value = instances.value[0]?.instance_id || "";
    }
    if (!healthInstanceId.value) healthInstanceId.value = instances.value[0]?.instance_id || "";
    await loadMetrics();
    if (
      outcome.server.type === "mirror" && canReadDeployment.value &&
      instances.value.some((item) => item.deployment_locked || item.state === "deploying") && !deploymentTask.value
    ) {
      await recoverActiveDeployment();
    }
  } catch (error) {
    loadError.value = error.message;
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) loading.value = false;
  }
}

function stateTagType(state) {
  if (state === "running") return "success";
  if (state === "failed") return "danger";
  if (state === "starting" || state === "stopping" || state === "deploying" || state === "deployment_pending") return "warning";
  return "info";
}

function canRun(instance, action) {
  if (
    !instance || instance.deployment_locked ||
    !hasPermission("instance." + action) || actionLoading[instance.instance_id]
  ) return false;
  if (action === "start") return instance.state === "stopped" || instance.state === "failed";
  if (action === "stop") return instance.state === "running";
  if (action === "restart") return instance.state === "running";
  if (action === "kill") return instance.state === "running" || instance.state === "stopping";
  return false;
}

async function confirmAction(instance, action) {
  if (action === "kill") {
    const result = await ElMessageBox.prompt(
      "强制关闭可能导致未保存的数据丢失。",
      "强制关闭子服",
      {
        type: "error",
        inputPlaceholder: "输入 " + instance.name + " 确认",
        inputValidator: (value) => value === instance.name || "子服名称不匹配",
        confirmButtonText: "强制关闭",
        cancelButtonText: "取消",
      },
    );
    return result.value === instance.name;
  }
  const online = Number(instance.online_players);
  const impact = Number.isFinite(online) ? `，当前在线玩家 ${online} 人` : "";
  await ElMessageBox.confirm(
    `确认${instanceActionLabels[action]}${instance.name}${impact}？`,
    instanceActionLabels[action] + "子服",
    { type: "warning", confirmButtonText: instanceActionLabels[action], cancelButtonText: "取消" },
  );
  return true;
}

async function invoke(instance, action) {
  if (!canRun(instance, action)) return;
  if (action !== "start") {
    try {
      if (!await confirmAction(instance, action)) return;
    } catch {
      return;
    }
  }
  actionLoading[instance.instance_id] = action;
  try {
    await request(
      `/api/v1/instances/${encodeURIComponent(instance.instance_id)}/${action}?node_id=${encodeURIComponent(nodeId.value)}`,
      { method: "POST", body: "{}" },
    );
    ElMessage.success("已提交" + instanceActionLabels[action] + "请求：" + instance.name);
    await load(true);
    if (action === "restart" && canViewPlugins.value) await loadPlugins(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    delete actionLoading[instance.instance_id];
  }
}

async function saveServer({ config }) {
  submitting.value = true;
  try {
    const result = await request(
      `/api/v1/servers/${encodeURIComponent(serverId.value)}?node_id=${encodeURIComponent(nodeId.value)}`,
      { method: "PUT", body: JSON.stringify(config) },
    );
    editorOpen.value = false;
    ElMessage.success(runningCount.value ? "配置已保存，运行中子服重启后完整生效" : "服务器配置已保存");
    if (result?.warnings?.length) ElMessage.warning(result.warnings.join("；"));
    await load();
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

async function removeServer() {
  if (!allStopped.value) {
    ElMessage.warning("请先关闭该服务器组的全部子服");
    return;
  }
  try {
    const result = await ElMessageBox.prompt(
      "删除会移除 daemon 上的服务器配置，工作目录和文件不会被删除。",
      "删除服务器组",
      {
        type: "error",
        inputPlaceholder: "输入 " + server.value.name + " 确认",
        inputValidator: (value) => value === server.value.name || "服务器组名称不匹配",
        confirmButtonText: "删除",
        cancelButtonText: "取消",
      },
    );
    if (result.value !== server.value.name) return;
    await request(
      `/api/v1/servers/${encodeURIComponent(serverId.value)}?node_id=${encodeURIComponent(nodeId.value)}`,
      { method: "DELETE" },
    );
    ElMessage.success("服务器组已删除");
    await router.replace({ name: "servers" });
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message);
  }
}

function stopDeploymentPolling() {
  if (deploymentTimer) {
    window.clearInterval(deploymentTimer);
    deploymentTimer = undefined;
  }
}

function startDeploymentPolling() {
  stopDeploymentPolling();
  deploymentTimer = window.setInterval(refreshDeployment, 1000);
}

function applyDeploymentMode(task) {
  if (task?.kind === "plugin_config_sync" || task?.kind === "mirror_deploy") {
    deploymentMode.value = task.kind;
  }
}

async function refreshDeployment() {
  const taskID = deploymentTask.value?.task_id;
  if (!taskID || deploymentRefreshing.value) return;
  deploymentRefreshing.value = true;
  try {
    const path = "/api/v1/deployments/" + encodeURIComponent(taskID) +
      "?node_id=" + encodeURIComponent(nodeId.value);
    deploymentTask.value = await request(path);
    applyDeploymentMode(deploymentTask.value);
    if (!deploymentActive.value) {
      stopDeploymentPolling();
      await load(true);
    }
  } catch (error) {
    stopDeploymentPolling();
    ElMessage.error(error.message);
  } finally {
    deploymentRefreshing.value = false;
  }
}

async function recoverActiveDeployment() {
  if (!canReadDeployment.value || deploymentRefreshing.value) return;
  deploymentRefreshing.value = true;
  try {
    const path = "/api/v1/servers/" + encodeURIComponent(serverId.value) +
      "/deployment?node_id=" + encodeURIComponent(nodeId.value);
    deploymentTask.value = await request(path);
    applyDeploymentMode(deploymentTask.value);
    startDeploymentPolling();
  } catch (error) {
    if (error.code !== "DEPLOYMENT_NOT_FOUND") ElMessage.error(error.message);
  } finally {
    deploymentRefreshing.value = false;
  }
}

async function openDeployment() {
  deploymentMode.value = "mirror_deploy";
  deploymentOpen.value = true;
  if (deploymentActive.value) {
    applyDeploymentMode(deploymentTask.value);
    return;
  }
  deploymentTask.value = null;
  if (instances.value.some((item) => item.deployment_locked || item.state === "deploying")) {
    await recoverActiveDeployment();
    if (deploymentTask.value) return;
  }
  resetDeployment();
}

async function openPluginConfigSync() {
  deploymentMode.value = "plugin_config_sync";
  deploymentOpen.value = true;
  if (deploymentActive.value) {
    applyDeploymentMode(deploymentTask.value);
    return;
  }
  deploymentTask.value = null;
  if (instances.value.some((item) => item.deployment_locked || item.state === "deploying")) {
    await recoverActiveDeployment();
    if (deploymentTask.value) return;
  }
  resetDeployment();
}

function deployableInstance(instance) {
  return !instance.deployment_locked && ["stopped", "running", "failed"].includes(instance.state);
}

function resetDeployment() {
  deploymentMode.value = deploymentMode.value || "mirror_deploy";
  stopDeploymentPolling();
  deploymentTask.value = null;
  deploymentTargets.value = instances.value
    .filter(deployableInstance)
    .map((item) => Number(item.slot))
    .filter(Number.isFinite);
}

async function startDeployment() {
  if (pluginConfigSyncMode.value) {
    await startPluginConfigSync();
    return;
  }
  if (!deploymentTargets.value.length) {
    ElMessage.warning("请至少选择一个部署目标");
    return;
  }
  const targetSet = new Set(deploymentTargets.value);
  const targets = instances.value.filter((item) => targetSet.has(Number(item.slot)));
  const running = targets.filter((item) => item.state === "running").length;
  const players = targets.reduce((total, item) => total + (Number(item.online_players) || 0), 0);
  try {
    await ElMessageBox.confirm(
      "将镜像目录部署到 " + targets.length + " 个子服。运行中的子服会先关闭，完成后自动恢复。" +
        (running ? " 当前运行 " + running + " 个子服" : "") +
        (players ? "，在线玩家 " + players + " 人" : "") + "。",
      "部署镜像服务器组",
      { type: "warning", confirmButtonText: "开始部署", cancelButtonText: "取消" },
    );
  } catch {
    return;
  }
  deploymentSubmitting.value = true;
  try {
    const path = "/api/v1/servers/" + encodeURIComponent(serverId.value) +
      "/deploy?node_id=" + encodeURIComponent(nodeId.value);
    deploymentTask.value = await request(path, {
      method: "POST", body: JSON.stringify({ targets: deploymentTargets.value }),
    });
    ElMessage.success("部署任务已创建");
    startDeploymentPolling();
    await load(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    deploymentSubmitting.value = false;
  }
}

async function startPluginConfigSync() {
  if (!deploymentTargets.value.length) {
    ElMessage.warning("请至少选择一个同步目标");
    return;
  }
  // 自适应检测：针对插件服（plugins/）与 mod 服（config/）检测镜像源中可同步的配置目录
  let detect = null;
  try {
    const detectPath = "/api/v1/servers/" + encodeURIComponent(serverId.value) +
      "/plugin-config-sync/detect?node_id=" + encodeURIComponent(nodeId.value);
    detect = await request(detectPath, { method: "GET" });
  } catch (error) {
    ElMessage.error("配置目录检测失败：" + error.message);
    return;
  }
  const dirs = Array.isArray(detect?.recommended) ? detect.recommended : [];
  const issues = Array.isArray(detect?.issues) ? detect.issues : [];
  const noConfigFound = issues.some((item) => item.code === "NO_CONFIG_DIR_FOUND");
  const partialMissing = issues.some((item) =>
    item.code === "MOD_CONFIG_DIR_MISSING" || item.code === "PLUGINS_DIR_MISSING");
  const issueText = issues.map((item) => item.message).join("\n");

  if (noConfigFound) {
    // 无法确定配置位置 → 弹窗说明情况并阻止同步
    await ElMessageBox.alert(
      issueText + "\n\n已取消同步，请先在镜像源中创建对应的配置目录（插件服为 plugins/，mod 服为 config/）。",
      "无法确定配置位置",
      { type: "warning", confirmButtonText: "知道了" },
    );
    return;
  }
  if (partialMissing) {
    // 插件服缺 plugins/ 或 mod 服缺 config/：说明情况，让用户决定是否同步剩余目录
    try {
      await ElMessageBox.confirm(
        issueText + "\n\n是否仍要继续同步检测到的目录？",
        "部分配置目录缺失",
        { type: "warning", confirmButtonText: "仅同步检测到的目录", cancelButtonText: "取消" },
      );
    } catch {
      return;
    }
  }
  const dirText = dirs.length ? dirs.join(", ") : "（无）";
  const targetSet = new Set(deploymentTargets.value);
  const targets = instances.value.filter((item) => targetSet.has(Number(item.slot)));
  try {
    await ElMessageBox.confirm(
      "将镜像源 " + dirText + " 目录中白名单后缀的文件覆盖到 " + targets.length + " 个实例，不会停止或重启服务器，也不会删除目标中的额外文件。同步完成后请按需执行重载命令。",
      "同步配置",
      { type: "warning", confirmButtonText: "开始同步", cancelButtonText: "取消" },
    );
  } catch {
    return;
  }
  deploymentSubmitting.value = true;
  try {
    const path = "/api/v1/servers/" + encodeURIComponent(serverId.value) +
      "/plugin-config-sync?node_id=" + encodeURIComponent(nodeId.value);
    deploymentTask.value = await request(path, {
      method: "POST", body: JSON.stringify({ targets: deploymentTargets.value, directories: dirs }),
    });
    ElMessage.success("配置同步任务已创建");
    startDeploymentPolling();
    await load(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    deploymentSubmitting.value = false;
  }
}

async function stopDeployment(force) {
  if (!deploymentTask.value || !deploymentActive.value) return;
  if (pluginConfigSyncMode.value) {
    await stopPluginConfigSync(force);
    return;
  }
  try {
    await ElMessageBox.confirm(
      force
        ? "强制结束可能使当前目标保持停止状态，但文件交换仍会在安全边界回滚。"
        : "取消会在当前安全点停止后续部署。",
      force ? "强制结束部署" : "取消部署",
      {
        type: force ? "error" : "warning",
        confirmButtonText: force ? "强制结束" : "取消部署",
        cancelButtonText: "返回",
      },
    );
  } catch {
    return;
  }
  deploymentSubmitting.value = true;
  try {
    const action = force ? "force-stop" : "cancel";
    const path = "/api/v1/deployments/" + encodeURIComponent(deploymentTask.value.task_id) +
      "/" + action + "?node_id=" + encodeURIComponent(nodeId.value);
    deploymentTask.value = await request(path, { method: "POST", body: "{}" });
    startDeploymentPolling();
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    deploymentSubmitting.value = false;
  }
}

async function stopPluginConfigSync(force) {
  try {
    await ElMessageBox.confirm(
      force ? "强制结束会停止后续配置文件同步，已经完成的目标不会回滚。" : "取消会在当前文件安全边界停止后续同步。",
      force ? "强制结束配置同步" : "取消配置同步",
      {
        type: force ? "error" : "warning",
        confirmButtonText: force ? "强制结束" : "取消同步",
        cancelButtonText: "返回",
      },
    );
  } catch {
    return;
  }
  deploymentSubmitting.value = true;
  try {
    const action = force ? "force-stop" : "cancel";
    const path = "/api/v1/deployments/" + encodeURIComponent(deploymentTask.value.task_id) +
      "/" + action + "?node_id=" + encodeURIComponent(nodeId.value);
    deploymentTask.value = await request(path, { method: "POST", body: "{}" });
    startDeploymentPolling();
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    deploymentSubmitting.value = false;
  }
}

function formatDate(value) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit",
  }).format(new Date(value));
}

function formatDuration(value) {
  if (!value) return "-";
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000));
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days) return days + " 天 " + hours + " 小时";
  if (hours) return hours + " 小时 " + minutes + " 分钟";
  return minutes + " 分钟";
}

function percent(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number.toFixed(1) + "%" : "--";
}

function memory(value) {
  const number = Number(value);
  return Number.isFinite(number) ? (number / 1024 / 1024 / 1024).toFixed(2) + " GB" : "--";
}

function formatBytes(value) {
  const bytes = Number(value) || 0;
  if (bytes >= 1024 ** 3) return (bytes / 1024 ** 3).toFixed(2) + " GB";
  if (bytes >= 1024 ** 2) return (bytes / 1024 ** 2).toFixed(2) + " MB";
  if (bytes >= 1024) return (bytes / 1024).toFixed(1) + " KB";
  return bytes + " B";
}

function gameValue(value, digits = 0, suffix = "") {
  const number = Number(value);
  return Number.isFinite(number) ? number.toFixed(digits) + suffix : "--";
}

onMounted(() => {
  load();
  window.addEventListener("beforeunload", beforeWindowUnload);
  refreshTimer = window.setInterval(() => load(true), 5000);
});
onBeforeUnmount(() => {
  window.clearInterval(refreshTimer);
  stopDeploymentPolling();
  window.removeEventListener("beforeunload", beforeWindowUnload);
  pluginConflictResolver?.("skip");
  pluginConflictResolver = null;
});

watch(pluginInstanceId, () => loadPlugins());
watch(activeTab, (value) => {
  if (value === "plugins") loadPlugins();
  if (value === "config") loadSyncConfiguration();
});

onBeforeRouteLeave(async () => {
  if (!pluginRestartRequired.value) return true;
  try {
    await ElMessageBox.confirm(
      "插件文件已变更，需要手动重启子服才能应用。确认离开当前页面？",
      "插件变更待重启",
      { type: "warning", confirmButtonText: "离开", cancelButtonText: "留在页面" },
    );
    return true;
  } catch {
    return false;
  }
});
</script>

<template>
  <div v-loading="loading" class="content-stack">
    <div class="page-toolbar detail-toolbar">
      <div class="detail-title">
        <el-tooltip content="返回服务器列表">
          <button class="icon-control" type="button" aria-label="返回服务器列表" @click="router.push({ name: 'servers' })">
            <ArrowLeft :size="18" />
          </button>
        </el-tooltip>
        <span class="node-symbol"><Server :size="18" /></span>
        <div><h2>{{ server?.name || "服务器组" }}</h2><p>{{ serverId }} · {{ nodeName }}</p></div>
      </div>
      <div class="toolbar-actions">
        <el-tooltip content="刷新">
          <el-button class="square-button" :loading="loading" aria-label="刷新" @click="load()">
            <RefreshCw v-if="!loading" :size="16" />
          </el-button>
        </el-tooltip>
        <el-button
          v-if="server?.type === 'mirror' && canDeploy"
          type="primary"
          :plain="!deploymentActive"
          @click="openDeployment"
        ><Upload :size="16" />{{ deploymentActive || deploymentGroupLocked ? "查看部署" : "部署镜像" }}</el-button>
        <el-tooltip
          v-if="server?.type === 'mirror' && canDeploy"
          :content="'将镜像源目录同步到各实例：' + syncDirectoriesText"
        >
          <el-button
            plain
            @click="openPluginConfigSync"
          ><FileCode2 :size="16" />{{ deploymentActive && deploymentTask?.kind === 'plugin_config_sync' ? '查看配置同步' : '同步配置' }}</el-button>
        </el-tooltip>
        <el-button v-if="canConfigure" :disabled="groupBusy || deploymentGroupLocked" @click="editorOpen = true"><Edit3 :size="16" />编辑配置</el-button>
        <el-button v-if="canDelete" type="danger" plain :disabled="!allStopped" @click="removeServer">
          <Trash2 :size="16" />删除
        </el-button>
      </div>
    </div>

    <el-alert
      v-if="loadError"
      :title="loadError"
      type="error"
      :closable="false"
      show-icon
      class="detail-load-error"
    >
      <template #default>
        <el-button size="small" :loading="loading" @click="load()">刷新重试</el-button>
        <el-button size="small" @click="router.replace({ name: 'servers' })">返回列表</el-button>
      </template>
    </el-alert>

    <el-tabs v-model="activeTab" class="management-tabs server-detail-tabs">
      <el-tab-pane v-if="canReadConsole" label="控制台" name="console">
        <div class="instance-control-bar">
          <div class="instance-control-identity">
            <span>当前子服</span>
            <el-select v-model="selectedInstanceId" placeholder="选择子服">
              <el-option v-for="item in instances" :key="item.instance_id" :label="item.name" :value="item.instance_id" />
            </el-select>
            <el-tag v-if="selectedInstance" :type="stateTagType(displayState(selectedInstance))" effect="plain">
              {{ stateLabels[displayState(selectedInstance)] || displayState(selectedInstance) }}
            </el-tag>
          </div>
          <div v-if="selectedInstance" class="instance-lifecycle-actions">
            <el-button
              v-if="hasPermission('instance.start')"
              type="success"
              :loading="actionLoading[selectedInstance.instance_id] === 'start'"
              :disabled="!canRun(selectedInstance, 'start')"
              @click="invoke(selectedInstance, 'start')"
            ><Play :size="15" />启动</el-button>
            <el-button
              v-if="hasPermission('instance.restart')"
              :loading="actionLoading[selectedInstance.instance_id] === 'restart'"
              :disabled="!canRun(selectedInstance, 'restart')"
              @click="invoke(selectedInstance, 'restart')"
            ><RotateCw :size="15" />重启</el-button>
            <el-button
              v-if="hasPermission('instance.stop')"
              :loading="actionLoading[selectedInstance.instance_id] === 'stop'"
              :disabled="!canRun(selectedInstance, 'stop')"
              @click="invoke(selectedInstance, 'stop')"
            ><Square :size="14" />关闭</el-button>
            <el-button
              v-if="hasPermission('instance.kill')"
              type="danger"
              plain
              :loading="actionLoading[selectedInstance.instance_id] === 'kill'"
              :disabled="!canRun(selectedInstance, 'kill')"
              @click="invoke(selectedInstance, 'kill')"
            ><OctagonX :size="15" />强制关闭</el-button>
          </div>
        </div>

        <div v-if="selectedInstance" class="console-instance-meta">
          <div><span>端口</span><strong>{{ selectedInstance.configured_port }}</strong></div>
          <div><span>PID</span><strong>{{ selectedInstance.pid || "-" }}</strong></div>
          <div><span>运行时间</span><strong>{{ formatDuration(selectedInstance.started_at) }}</strong></div>
          <div><span>CPU</span><strong>{{ percent(selectedInstance.cpu_percent) }}</strong></div>
          <div><span>内存</span><strong>{{ memory(selectedInstance.memory_bytes) }}</strong></div>
          <div><span>玩家</span><strong>{{ gameValue(selectedInstance.online_players) }}</strong></div>
        </div>

        <ConsoleOutput
          v-if="selectedInstance"
          :key="selectedInstance.instance_id"
          :node-id="nodeId"
          :instance-id="selectedInstance.instance_id"
          :enabled="activeTab === 'console'"
          :can-command="canCommand"
          :running="selectedInstance.state === 'running' && !selectedInstance.deployment_locked"
        />
        <div v-else class="empty-state"><strong>暂无可操作的子服</strong></div>
      </el-tab-pane>

      <el-tab-pane label="概述" name="overview">
        <div class="instance-overview-grid">
          <article v-for="instance in instances" :key="instance.instance_id" class="instance-overview-card">
            <div class="instance-overview-head">
              <div class="node-cell">
                <span class="node-symbol"><Server :size="16" /></span>
                <div><strong>{{ instance.name }}</strong><small>{{ instance.instance_id }}</small></div>
              </div>
              <el-tag :type="stateTagType(displayState(instance))" effect="plain">{{ stateLabels[displayState(instance)] || displayState(instance) }}</el-tag>
            </div>
            <div class="instance-overview-primary">
              <div><Cpu :size="15" /><span>CPU</span><strong>{{ percent(instance.cpu_percent) }}</strong></div>
              <div><MemoryStick :size="15" /><span>内存</span><strong>{{ memory(instance.memory_bytes) }}</strong></div>
              <div><Activity :size="15" /><span>TPS</span><strong>{{ gameValue(instance.tps, 1) }}</strong></div>
              <div><Users :size="15" /><span>玩家</span><strong>{{ gameValue(instance.online_players) }}<template v-if="Number.isFinite(Number(instance.max_players))"> / {{ instance.max_players }}</template></strong></div>
            </div>
            <div class="instance-overview-facts">
              <div><span>端口</span><strong>{{ instance.configured_port }}</strong></div>
              <div><span>PID</span><strong>{{ instance.pid || "-" }}</strong></div>
              <div><span>运行时间</span><strong>{{ formatDuration(instance.started_at) }}</strong></div>
              <div><span>Prism 插件</span><strong :class="{ muted: !instance.plugin_connected }">{{ instance.plugin_connected ? "已连接" : "未连接" }}</strong></div>
            </div>
            <div class="instance-overview-foot">
              <span v-if="instance.pending_restart" class="pending-label">配置待重启</span>
              <span v-else-if="instance.last_error" class="danger">{{ instance.last_error }}</span>
              <span v-else class="muted">最后启动：{{ formatDate(instance.started_at) }}</span>
              <el-button
                v-if="canReadConsole"
                text
                @click="selectedInstanceId = instance.instance_id; activeTab = 'console'"
              ><Terminal :size="14" />控制台</el-button>
            </div>
          </article>
        </div>

        <section class="data-section server-health-section">
          <div class="section-title">
            <div><h3>运行趋势</h3><p>最近 10 分钟的子服占用与游戏状态</p></div>
            <el-select v-model="healthInstanceId" placeholder="选择子服">
              <el-option v-for="item in instances" :key="item.instance_id" :label="item.name" :value="item.instance_id" />
            </el-select>
          </div>
          <div class="metric-chart-grid server-metric-chart-grid">
            <MetricLineChart title="CPU" :points="cpuHistory" unit="%" :maximum="100" color="#397eaf" />
            <MetricLineChart title="内存" :points="memoryHistory" unit=" GB" :decimals="2" color="#2d8a60" />
            <MetricLineChart title="在线玩家" :points="playerHistory" :decimals="0" color="#9a681d" />
            <MetricLineChart title="TPS" :points="tpsHistory" :maximum="20" color="#8a5f9e" />
          </div>
        </section>

        <section v-if="canViewPlayers" class="data-section player-data-section">
          <div class="section-title"><div><h3>在线玩家</h3><p>{{ players.length }} 人在线</p></div></div>
          <el-table :data="players" row-key="uuid">
            <el-table-column label="玩家" min-width="180"><template #default="{ row }"><strong>{{ row.name || row.username }}</strong><small v-if="row.uuid" class="block muted">{{ row.uuid }}</small></template></el-table-column>
            <el-table-column label="当前服务器" min-width="150"><template #default="{ row }">{{ row.server_id || row.instance_name }}</template></el-table-column>
            <el-table-column label="延迟" width="100"><template #default="{ row }">{{ Number.isFinite(Number(row.ping)) ? row.ping + " ms" : "-" }}</template></el-table-column>
            <el-table-column label="加入时间" min-width="150"><template #default="{ row }">{{ formatDate(row.joined_at) }}</template></el-table-column>
            <el-table-column v-if="isProxyServer && canTransferPlayers" label="操作" width="100" align="right">
              <template #default="{ row }">
                <el-button type="primary" link @click="openTransfer(row)"><ArrowRightLeft :size="14" />转移</el-button>
              </template>
            </el-table-column>
            <template #empty><div class="table-empty"><Users :size="24" /><span>暂无玩家数据</span></div></template>
          </el-table>
        </section>
      </el-tab-pane>

      <el-tab-pane v-if="canReadFiles" label="文件" name="files" lazy>
        <FileManager
          v-if="server"
          :node-id="nodeId"
          :server="server"
          :instances="instances"
        />
      </el-tab-pane>

      <el-tab-pane v-if="canViewPlugins" :label="pluginData.directory === 'mods' ? '模组' : '插件'" name="plugins">
        <div class="plugin-toolbar">
          <div>
            <span>子服</span>
            <el-select v-model="pluginInstanceId" :disabled="pluginUploadState.active" placeholder="选择子服">
              <el-option v-for="item in instances" :key="item.instance_id" :label="item.name" :value="item.instance_id" />
            </el-select>
          </div>
          <el-input v-model="pluginSearch" clearable placeholder="搜索插件" />
          <div class="plugin-toolbar-status">
            <el-tag v-if="pluginData.directory" :type="pluginData.directory === 'mods' ? 'warning' : 'info'" effect="plain">
              {{ pluginData.directory === "mods" ? "mods 目录" : "plugins 目录" }}
            </el-tag>
            <el-tag :type="pluginData.plugin_connected ? 'success' : 'info'" effect="plain">
              <PlugZap :size="13" />{{ pluginData.plugin_connected ? "Prism 已连接" : "仅文件扫描" }}
            </el-tag>
            <el-tooltip v-if="canDeployPlugins" content="上传插件 JAR">
              <el-button
                class="square-button"
                :disabled="pluginUploadState.active || !pluginInstanceId"
                aria-label="上传插件 JAR"
                @click="choosePluginFiles"
              ><Upload :size="15" /></el-button>
            </el-tooltip>
            <el-tooltip content="刷新插件">
              <el-button class="square-button" :loading="pluginLoading" aria-label="刷新插件" @click="loadPlugins()">
                <RefreshCw v-if="!pluginLoading" :size="15" />
              </el-button>
            </el-tooltip>
          </div>
        </div>
        <el-alert
          v-if="pluginRestartPending"
          type="warning"
          :closable="false"
          show-icon
          :title="pluginPendingTitle"
          class="plugin-restart-alert"
        >
          <template #default>
            <el-button
              v-if="pluginInstance?.state === 'running' && hasPermission('instance.restart')"
              size="small"
              :loading="actionLoading[pluginInstance.instance_id] === 'restart'"
              @click="invoke(pluginInstance, 'restart')"
            ><RotateCw :size="14" />重启当前子服</el-button>
          </template>
        </el-alert>
        <div
          v-loading="pluginLoading"
          class="table-frame plugin-drop-frame"
          :class="{ 'drop-active': pluginDropActive }"
          @dragover="handlePluginDragOver"
          @dragleave="handlePluginDragLeave"
          @drop="handlePluginDrop"
        >
          <div v-if="pluginUploadState.active" class="plugin-upload-progress">
            <div>
              <span>{{ pluginUploadState.name }}</span>
              <strong>{{ pluginUploadState.completed }}/{{ pluginUploadState.total }}</strong>
            </div>
            <div v-if="pluginUploadState.progress.total" class="plugin-upload-progress-detail">
              <span>上传中 {{ pluginUploadState.progress.percent }}%（{{ formatBytes(pluginUploadState.progress.loaded) }} / {{ formatBytes(pluginUploadState.progress.total) }}）</span>
            </div>
            <el-progress
              :percentage="pluginUploadState.progress.total ? pluginUploadState.progress.percent : Math.round(pluginUploadState.completed / pluginUploadState.total * 100)"
              :stroke-width="3"
              :show-text="false"
            />
          </div>
          <el-table :data="filteredPlugins" :row-key="(row) => (row.source_file || 'runtime') + ':' + row.name">
            <el-table-column label="插件" min-width="230">
              <template #default="{ row }">
                <div class="node-cell"><span class="node-symbol"><Puzzle :size="16" /></span><div><strong>{{ row.name }}</strong><small>{{ row.main || row.runtime_main || "-" }}</small></div></div>
              </template>
            </el-table-column>
            <el-table-column label="文件 / 运行版本" min-width="150">
              <template #default="{ row }">
                <code>{{ row.version || "-" }}</code>
                <small v-if="row.runtime_version && row.runtime_version !== row.version" class="block danger">运行 {{ row.runtime_version }}</small>
              </template>
            </el-table-column>
            <el-table-column label="来源文件" min-width="180">
              <template #default="{ row }"><code class="plugin-source-file">{{ row.source_file || "文件已移除" }}</code></template>
            </el-table-column>
            <el-table-column label="校验" width="100">
              <template #default="{ row }">
                <el-tooltip :content="row.issues?.includes('file_changed_since_start') ? '启动后插件 JAR 已发生变化' : (row.sha256 || '无文件摘要')">
                  <el-tag :type="row.issues?.includes('file_changed_since_start') ? 'danger' : (row.sha256 ? 'success' : 'info')" effect="plain">
                    {{ row.issues?.includes("file_changed_since_start") ? "已变更" : (row.sha256 ? "已校验" : "未知") }}
                  </el-tag>
                </el-tooltip>
              </template>
            </el-table-column>
            <el-table-column label="状态" min-width="140">
              <template #default="{ row }">
                <el-tag :type="row.status === 'loaded' ? 'success' : (row.status === 'conflict' ? 'danger' : (row.pending_restart ? 'warning' : 'info'))" effect="plain">
                  {{ pluginStatusLabels[row.status] || row.status }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="190" align="right">
              <template #default="{ row }">
                <el-button
                  v-if="canDeployPlugins && row.file_present"
                  type="primary"
                  link
                  :loading="pluginActionLoading === row.name"
                  @click="setPluginEnabled(row, !row.enabled)"
                >{{ row.enabled ? "禁用" : "启用" }}</el-button>
                <el-button
                  v-if="canRemovePlugins && row.file_present"
                  type="danger"
                  link
                  :disabled="Boolean(pluginActionLoading)"
                  @click="openUninstall(row)"
                ><Trash2 :size="14" />卸载</el-button>
              </template>
            </el-table-column>
            <template #empty><div class="table-empty"><Puzzle :size="24" /><span>暂无插件数据</span></div></template>
          </el-table>
        </div>
        <input ref="pluginUploadInput" type="file" accept=".jar,application/java-archive" multiple hidden @change="handlePluginFileInput" />
      </el-tab-pane>

      <el-tab-pane label="配置" name="config">
        <section v-if="server" class="data-section">
          <div class="section-title"><div><h3>运行配置</h3><p>由 daemon 保存并用于派生子服</p></div></div>
          <el-descriptions :column="2" border class="detail-descriptions">
            <el-descriptions-item label="服务器组 ID"><code>{{ server.server_id }}</code></el-descriptions-item>
            <el-descriptions-item label="类型">{{ isProxyServer ? "代理服" : (server.type === "mirror" ? "镜像服" : "普通服务器") }}</el-descriptions-item>
            <el-descriptions-item label="平台">{{ server.platform || "paper" }}</el-descriptions-item>
            <el-descriptions-item :label="server.type === 'mirror' ? '根目录' : '工作目录'"><code>{{ server.type === "mirror" ? server.root_path : server.workspace }}</code></el-descriptions-item>
            <el-descriptions-item label="端口"><code>{{ server.type === "mirror" ? server.ports.join(", ") : server.port }}</code></el-descriptions-item>
            <el-descriptions-item label="启动命令"><code>{{ server.process.start_command }}</code></el-descriptions-item>
            <el-descriptions-item label="停止命令"><code>{{ server.process.stop_command }}</code></el-descriptions-item>
            <el-descriptions-item label="停止超时">{{ server.process.stop_timeout_seconds }} 秒</el-descriptions-item>
            <el-descriptions-item label="自动策略">{{ server.process.auto_start ? "自动启动" : "手动启动" }} · {{ server.process.auto_restart ? "异常自动重启" : "不自动重启" }}</el-descriptions-item>
            <el-descriptions-item label="控制台编码">{{ server.console.encoding.toUpperCase() }}</el-descriptions-item>
          </el-descriptions>
        </section>
        <section v-if="server && canConfigure" v-loading="proxyConfigLoading" class="data-section">
          <template v-if="isProxyServer">
            <div class="section-title">
              <div><h3>下游服务器同步</h3><p>节点选择会自动包含该节点以后新增的兼容服务器</p></div>
              <el-button type="primary" :loading="proxyConfigLoading" @click="saveProxyRules">保存并同步</el-button>
            </div>
            <TargetSelectionTree v-model="proxyRules" :nodes="allNodeContents" exclude-proxy />
            <el-alert
              v-if="proxyStatus"
              class="plugin-restart-alert"
              :type="proxyStatus.state === 'failed' ? 'error' : (proxyStatus.state === 'synced' ? 'success' : 'info')"
              :closable="false"
              :title="'同步状态：' + proxyStatus.state + (proxyStatus.error ? ' · ' + proxyStatus.error : '')"
            />
          </template>
          <template v-else>
            <div class="section-title">
              <div><h3>同步到代理服</h3><p>停止状态仍会保留在代理服务器列表中</p></div>
              <el-button type="primary" :loading="proxyConfigLoading" @click="saveProxySelections">保存</el-button>
            </div>
            <div class="proxy-selection-list">
              <el-checkbox
                v-for="item in proxySelections"
                :key="item.node_id + ':' + item.server_id"
                v-model="item.selected"
                border
              >{{ item.server_id }}</el-checkbox>
              <span v-if="!proxySelections.length" class="muted">暂无已配置的代理服</span>
            </div>
          </template>
        </section>
      </el-tab-pane>
    </el-tabs>
  </div>

  <el-dialog
    v-model="pluginConflictOpen"
    title="发现同名插件"
    width="min(560px, 94vw)"
    :show-close="false"
    :close-on-click-modal="false"
    :close-on-press-escape="false"
  >
    <div class="plugin-conflict-content">
      <p>
        上传文件「{{ pluginConflict.fileName }}」识别为
        <strong>{{ pluginConflict.incoming.name }} {{ pluginConflict.incoming.version }}</strong>。
      </p>
      <p>
        当前子服「{{ pluginInstance?.name || pluginInstanceId }}」已安装
        <strong>{{ pluginConflict.incoming.name }} {{ pluginConflict.existing.version }}</strong>
        （{{ pluginConflict.existing.file }}）。
      </p>
      <small>{{ pluginConflictRestartNote }}</small>
    </div>
    <template #footer>
      <el-button @click="decidePluginConflict('skip')">跳过此插件</el-button>
      <el-button type="primary" plain @click="decidePluginConflict('replace')">仅替换</el-button>
      <el-button
        v-if="canRestartUploadedPlugin"
        type="primary"
        @click="decidePluginConflict('replace-restart')"
      >
        {{ pluginUploadState.total > 1 ? "替换并在完成后重启" : "替换并重启" }}
      </el-button>
    </template>
  </el-dialog>

  <el-dialog
    v-model="deploymentOpen"
    :title="pluginConfigSyncMode ? '同步配置' : '部署镜像服务器组'"
    width="min(820px, 94vw)"
    :close-on-click-modal="false"
  >
    <div v-if="!deploymentTask" class="deployment-setup">
      <div class="deployment-source">
        <span>镜像目录</span>
        <code>{{ server?.root_path }} / {{ server?.image_directory }}</code>
      </div>
      <div v-if="pluginConfigSyncMode" class="deployment-source">
        <span>同步目录</span>
        <code>{{ syncDirectoriesText }}</code>
      </div>
      <div v-if="pluginConfigSyncMode" class="deployment-source">
        <span>同步后缀</span>
        <code>{{ (server?.plugin_config_sync_extensions || []).join(', ') }}</code>
      </div>
      <div class="section-title">
        <div>
          <h3>{{ pluginConfigSyncMode ? '同步目标' : '部署目标' }}</h3>
          <p v-if="pluginConfigSyncMode">仅覆盖镜像源 {{ syncDirectoriesText }} 目录中符合白名单的文件，不会停止或重启服务器，也不会删除目标中的额外文件。</p>
          <p v-else>选中的子服在任务结束前会被锁定，运行中的子服完成后自动恢复</p>
        </div>
        <el-checkbox
          :model-value="deploymentTargets.length === instances.filter(deployableInstance).length"
          :indeterminate="deploymentTargets.length > 0 && deploymentTargets.length < instances.filter(deployableInstance).length"
          @change="deploymentTargets = $event ? instances.filter(deployableInstance).map((item) => Number(item.slot)) : []"
        >全选</el-checkbox>
      </div>
      <el-checkbox-group v-model="deploymentTargets" class="deployment-target-grid">
        <el-checkbox
          v-for="item in instances"
          :key="item.instance_id"
          :value="Number(item.slot)"
          :disabled="!deployableInstance(item)"
          border
        >
          <span>{{ item.name }}</span>
          <small>{{ stateLabels[displayState(item)] || displayState(item) }} · {{ item.online_players || 0 }} 人</small>
        </el-checkbox>
      </el-checkbox-group>
    </div>

    <div v-else class="deployment-task">
      <div class="deployment-task-head">
        <div>
          <span>任务状态</span>
            <strong>{{ deploymentStatusText }}</strong>
        </div>
        <div>
          <span>当前目标</span>
          <strong>{{ deploymentTask.current_instance || "等待调度" }}</strong>
        </div>
        <div>
          <span>完成情况</span>
          <strong>{{ deploymentTask.completed }} / {{ deploymentTask.targets?.length || 0 }}</strong>
        </div>
        <div>
          <span>失败</span>
          <strong :class="{ danger: deploymentTask.failed }">{{ deploymentTask.failed || 0 }}</strong>
        </div>
      </div>
      <el-progress
        :percentage="deploymentProgress"
        :status="deploymentTask.status === 'completed' ? 'success' : (deploymentTask.failed ? 'exception' : undefined)"
      />
      <div v-if="deploymentTask.copy_stage" class="deployment-copy-progress">
        <div class="deployment-copy-head">
          <div>
            <span>当前文件阶段</span>
            <strong>{{ deploymentCopyStageLabels[deploymentTask.copy_stage] || deploymentTask.copy_stage }}</strong>
          </div>
          <div>
            <span>文件</span>
            <strong>{{ deploymentTask.copy_files_done || 0 }} / {{ deploymentTask.copy_files_total || 0 }}</strong>
          </div>
          <div>
            <span>数据量</span>
            <strong>{{ formatBytes(deploymentTask.copy_bytes_done) }} / {{ formatBytes(deploymentTask.copy_bytes_total) }}</strong>
          </div>
          <div>
            <span>并发</span>
            <strong>{{ deploymentTask.copy_concurrency || 1 }}</strong>
          </div>
        </div>
        <el-progress
          :percentage="deploymentCopyProgress"
          :stroke-width="8"
          :show-text="false"
          :status="deploymentTask.failed ? 'exception' : undefined"
        />
      </div>
      <div class="deployment-log">
        <div v-for="log in deploymentTask.logs || []" :key="log.sequence" :class="'deployment-log-' + log.level">
          <time>{{ formatDate(log.timestamp) }}</time>
          <code>{{ log.instance_id || "任务" }}</code>
          <span>{{ log.message }}</span>
        </div>
        <div v-if="!deploymentTask.logs?.length" class="deployment-log-empty">等待部署日志</div>
      </div>
    </div>

    <template #footer>
      <template v-if="!deploymentTask">
        <el-button @click="deploymentOpen = false">取消</el-button>
        <el-button type="primary" :loading="deploymentSubmitting" :disabled="!deploymentTargets.length" @click="startDeployment">
          <FileCode2 v-if="pluginConfigSyncMode" :size="15" />
          <Upload v-else :size="15" />
          {{ pluginConfigSyncMode ? '开始同步' : '开始部署' }}
        </el-button>
      </template>
      <template v-else-if="deploymentActive">
        <el-button @click="deploymentOpen = false">后台运行</el-button>
        <el-button v-if="canCancelTasks" :loading="deploymentSubmitting" @click="stopDeployment(false)">{{ pluginConfigSyncMode ? '取消同步' : '取消部署' }}</el-button>
        <el-button v-if="canCancelTasks" type="danger" plain :loading="deploymentSubmitting" @click="stopDeployment(true)">强制结束</el-button>
      </template>
      <template v-else>
        <el-button @click="deploymentOpen = false">关闭</el-button>
        <el-button type="primary" plain @click="resetDeployment">{{ pluginConfigSyncMode ? '再次同步' : '再次部署' }}</el-button>
      </template>
    </template>
  </el-dialog>

  <ServerEditorDialog
    v-model="editorOpen"
    :server="server"
    :node-id="nodeId"
    :submitting="submitting"
    @submit="saveServer"
  />

  <el-dialog v-model="uninstallOpen" title="卸载插件" width="min(480px, 94vw)">
    <div class="uninstall-plugin-summary">
      <span class="node-symbol"><Puzzle :size="16" /></span>
      <div><strong>{{ uninstallTarget?.name }}</strong><small>{{ uninstallTarget?.source_file }}</small></div>
    </div>
    <el-form label-position="top">
      <el-form-item>
        <el-checkbox v-model="uninstallForm.deleteConfig">同时删除插件配置目录</el-checkbox>
      </el-form-item>
      <el-form-item v-if="uninstallForm.deleteConfig" label="配置目录名" required>
        <el-input v-model="uninstallForm.configDirectory" maxlength="100" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="uninstallOpen = false">取消</el-button>
      <el-button type="danger" :loading="pluginActionLoading === uninstallTarget?.name" @click="uninstallPlugin">
        <Trash2 :size="15" />卸载
      </el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="transferOpen" title="转移玩家" width="min(480px, 94vw)">
    <el-form label-position="top">
      <el-form-item label="玩家">
        <el-input :model-value="transferForm.player?.name" disabled />
      </el-form-item>
      <el-form-item label="目标服务器" required>
        <el-select v-model="transferForm.targetServerId" class="full-control" filterable>
          <el-option
            v-for="item in backendOptions"
            :key="item.value"
            :label="item.label + ' · ' + item.nodeName"
            :value="item.value"
          />
        </el-select>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="transferOpen = false">取消</el-button>
      <el-button type="primary" :loading="transferSubmitting" @click="transferPlayer">
        <ArrowRightLeft :size="15" />转移
      </el-button>
    </template>
  </el-dialog>
</template>
