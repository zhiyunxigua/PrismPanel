<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref } from "vue";
import {
  Download, FolderOpen, Gamepad2, ListChecks, LoaderCircle, Package, Plus, Puzzle, RefreshCw, Search, Settings, Square, Trash2, UserRound, X,
} from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  isWinApp,
  mcAuthStatus,
  mcAvailableVersions,
  mcAddDownload,
  mcCancelDownload,
  mcClearDownloads,
  mcCloseGame,
  mcDeleteVersion,
  mcDownloadList,
  mcFabricLoaders,
  mcGetVersionSettings,
  mcInstalledVersions,
  mcIsFabricInstalled,
  mcLaunch,
  mcLaunchProgress,
  mcLogout,
  mcModrinthInstall,
  mcModsDelete,
  mcModsList,
  mcModsOpenDir,
  mcModsToggle,
  mcPollDeviceLogin,
  mcRemoveDownload,
  mcSaveVersionSettings,
  mcSearchModrinth,
  mcSetOfflineAccount,
  mcStartDeviceLogin,
  mcThirdPartyLogin,
  selectJavaExecutable,
  selectMCGameDirectory,
} from "../runtime";

const loading = ref(false);
const account = ref(null);
const installed = ref([]);
const available = ref([]);
const availableLoading = ref(false);
const offlineName = ref("");
const installing = ref("");

const accountDialogOpen = ref(false);
const deviceLogin = ref(null);
const devicePolling = ref(false);

const addVersionDialogOpen = ref(false);
const addVersionID = ref("");

const fabricDialogOpen = ref(false);
const fabricTarget = ref("");
const fabricLoader = ref("");
const fabricLoaders = ref([]);
const fabricLoadersLoading = ref(false);

const launchForm = reactive({ version: "", server_ip: "127.0.0.1", server_port: 25565, instance_dir: "", max_memory_mb: 2048 });
const launching = ref(false);
const progress = ref(null);
const progressTimer = 0;

const fabricByBase = ref({});
const deletingVersion = ref("");
const settingsDialogOpen = ref(false);
const settingsVersion = ref("");
const settingsSubmitting = ref(false);
const settingsForm = reactive({ server_ip: "127.0.0.1", server_port: 25565, max_memory_mb: 2048, instance_dir: "", jvm_args: "", use_fabric: false, launch_version: "", java_path: "", width: 0, height: 0 });
const launchVersionOptions = computed(() => {
  const base = settingsVersion.value;
  return installed.value.filter((item) => item.fabric && item.id.endsWith("-" + base));
});
const thirdPartySubmitting = ref(false);
const thirdPartyForm = reactive({ server: "", username: "", password: "" });
const modsDialogOpen = ref(false);
const modsVersion = ref("");
const modsList = ref([]);
const modsLoading = ref(false);
const modsSearchQuery = ref("");
const modsSearchResults = ref([]);
const modsSearching = ref(false);
const modsInstalling = ref("");

function modeLabel(mode) {
  if (mode === "microsoft") return "微软";
  if (mode === "third_party") return "第三方";
  return "离线";
}

const hasAccount = computed(() => Boolean(account.value?.name));
const accountLabel = computed(() => {
  if (!account.value) return "未设置账号";
  return `${modeLabel(account.value.mode)} · ${account.value.name}`;
});
const runningVersion = ref("");
const downloadQueueOpen = ref(false);
const downloadTasks = ref([]);
const downloadListener = ref(null);
const downloadActiveCount = computed(() =>
  downloadTasks.value.filter((task) => task.status === "queued" || task.status === "downloading").length,
);

let progressInterval = 0;

onMounted(() => {
  loadPage();
  refreshDownloads();
  downloadListener.value = window.runtime?.EventsOn?.("prism:mc-download", (task) => {
    const index = downloadTasks.value.findIndex((item) => item.id === task.id);
    if (index >= 0) downloadTasks.value[index] = task;
    else downloadTasks.value.push(task);
  });
});
onBeforeUnmount(() => {
  if (progressInterval) window.clearInterval(progressInterval);
  if (downloadListener.value) {
    if (window.runtime && typeof window.runtime.EventsOff === "function") {
      window.runtime.EventsOff("prism:mc-download", downloadListener.value);
    }
    downloadListener.value = null;
  }
});

async function loadPage() {
  loading.value = true;
  try {
    const [accountResult, installedResult] = await Promise.all([
      isWinApp() ? mcAuthStatus() : Promise.resolve(null),
      isWinApp() ? mcInstalledVersions() : Promise.resolve([]),
    ]);
    account.value = accountResult || null;
    installed.value = installedResult || [];
    if (account.value?.mode === "offline") offlineName.value = account.value.name || "";
    if (launchForm.version && !installed.value.some((item) => item.id === launchForm.version)) launchForm.version = "";
    fabricByBase.value = {};
    const bases = installed.value.filter((item) => !item.fabric).map((item) => item.id);
    await Promise.all(bases.map(async (base) => {
      try {
        fabricByBase.value[base] = await mcIsFabricInstalled(base);
      } catch {
        fabricByBase.value[base] = false;
      }
    }));
  } catch (error) {
    ElMessage.error(error.message || "加载加入游戏配置失败");
  } finally {
    loading.value = false;
  }
}

// ---------- 账号 ----------
function openAccountDialog() {
  offlineName.value = account.value?.mode === "offline" ? account.value.name || "" : "";
  accountDialogOpen.value = true;
}

async function submitOffline() {
  if (!offlineName.value.trim()) {
    ElMessage.warning("请输入离线玩家名");
    return;
  }
  try {
    account.value = await mcSetOfflineAccount(offlineName.value.trim());
    ElMessage.success("离线账号已设置");
    accountDialogOpen.value = false;
  } catch (error) {
    ElMessage.error(error.message || "设置离线账号失败");
  }
}

async function startMicrosoftLogin() {
  try {
    deviceLogin.value = await mcStartDeviceLogin();
    devicePolling.value = true;
    pollDeviceLogin();
  } catch (error) {
    ElMessage.error(error.message || "发起微软登录失败");
  }
}

function pollDeviceLogin() {
  if (progressInterval) window.clearInterval(progressInterval);
  progressInterval = window.setInterval(async () => {
    try {
      const result = await mcPollDeviceLogin(deviceLogin.value.state_id);
      stopDevicePolling();
      account.value = result;
      deviceLogin.value = null;
      accountDialogOpen.value = false;
      ElMessage.success("微软账号登录成功");
    } catch (error) {
      if (error.message && error.message.includes("authorization_pending")) return;
      stopDevicePolling();
      deviceLogin.value = null;
      ElMessage.error(error.message || "微软登录失败");
    }
  }, 3000);
}

function stopDevicePolling() {
  devicePolling.value = false;
  if (progressInterval) window.clearInterval(progressInterval);
  progressInterval = 0;
}

async function logoutAccount() {
  try {
    await ElMessageBox.confirm("删除本地保存的国际版账号？", "退出登录", {
      type: "warning", confirmButtonText: "退出", cancelButtonText: "取消",
    });
    await mcLogout();
    account.value = null;
    ElMessage.success("已退出登录");
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message || "退出失败");
  }
}

// ---------- 版本 ----------
async function openAddVersion() {
  addVersionID.value = "";
  addVersionDialogOpen.value = true;
  if (!available.value.length) await refreshAvailable();
}

async function refreshAvailable() {
  availableLoading.value = true;
  try {
    available.value = await mcAvailableVersions();
  } catch (error) {
    ElMessage.error(error.message || "拉取 Mojang 版本列表失败");
  } finally {
    availableLoading.value = false;
  }
}

async function submitAddVersion() {
  const id = addVersionID.value.trim();
  if (!id) {
    ElMessage.warning("请选择或输入游戏版本");
    return;
  }
  if (installed.value.some((item) => item.id === id)) {
    ElMessage.info("该版本已安装");
    addVersionDialogOpen.value = false;
    return;
  }
  await enqueueDownload("version", id, "");
}

async function enqueueDownload(kind, id, loader) {
  try {
    await mcAddDownload(kind, id, loader);
    addVersionDialogOpen.value = false;
    fabricDialogOpen.value = false;
    ElMessage.success("已加入下载队列");
    await refreshDownloads();
    downloadQueueOpen.value = true;
  } catch (error) {
    ElMessage.error(error.message || "加入下载队列失败");
  }
}

async function refreshDownloads() {
  try {
    downloadTasks.value = await mcDownloadList() || [];
  } catch (error) {
    ElMessage.error(error.message || "读取下载队列失败");
  }
}

async function cancelDownload(task) {
  try {
    await mcCancelDownload(task.id);
    await refreshDownloads();
  } catch (error) {
    ElMessage.error(error.message || "取消下载失败");
  }
}

async function removeDownload(task) {
  try {
    await mcRemoveDownload(task.id);
    await refreshDownloads();
  } catch (error) {
    ElMessage.error(error.message || "移除下载任务失败");
  }
}

async function clearFinishedDownloads() {
  try {
    await mcClearDownloads();
    await refreshDownloads();
  } catch (error) {
    ElMessage.error(error.message || "清空下载队列失败");
  }
}

function queueStatusLabel(task) {
  const labels = { queued: "排队中", downloading: "下载中", done: "已完成", failed: "失败", canceled: "已取消" };
  return labels[task.status] || task.status;
}

async function deleteVersion(version) {
  deletingVersion.value = version.id;
  try {
    await ElMessageBox.confirm(
      `删除版本 ${version.id}？其独立的 .minecraft（mods/config 等）将一并删除，不可恢复。`,
      "删除版本",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" },
    );
    await mcDeleteVersion(version.id);
    ElMessage.success("版本已删除");
    await loadPage();
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message || "删除失败");
  } finally {
    deletingVersion.value = "";
  }
}

async function openVersionSettings(version) {
  settingsVersion.value = version.id;
  Object.assign(settingsForm, {
    server_ip: "127.0.0.1", server_port: 25565, max_memory_mb: 2048, instance_dir: "", jvm_args: "",
    use_fabric: false, launch_version: "", java_path: "", width: 0, height: 0,
  });
  try {
    const saved = await mcGetVersionSettings(version.id);
    if (saved) {
      Object.assign(settingsForm, {
        server_ip: saved.server_ip || "127.0.0.1",
        server_port: Number(saved.server_port) || 25565,
        max_memory_mb: Number(saved.max_memory_mb) || 2048,
        instance_dir: saved.instance_dir || "",
        jvm_args: saved.jvm_args || "",
        use_fabric: Boolean(saved.use_fabric),
        launch_version: saved.launch_version || "",
        java_path: saved.java_path || "",
        width: Number(saved.width) || 0,
        height: Number(saved.height) || 0,
      });
    }
  } catch (error) {
    ElMessage.error(error.message || "读取版本设置失败");
  }
  settingsDialogOpen.value = true;
}

async function saveVersionSettings() {
  settingsSubmitting.value = true;
  try {
    await mcSaveVersionSettings(settingsVersion.value, {
      server_ip: settingsForm.server_ip.trim(),
      server_port: Number(settingsForm.server_port) || 25565,
      max_memory_mb: Number(settingsForm.max_memory_mb) || 2048,
      instance_dir: settingsForm.instance_dir.trim(),
      jvm_args: settingsForm.jvm_args.trim(),
      use_fabric: Boolean(settingsForm.use_fabric),
      launch_version: settingsForm.launch_version.trim(),
      java_path: settingsForm.java_path.trim(),
      width: Number(settingsForm.width) || 0,
      height: Number(settingsForm.height) || 0,
    });
    settingsDialogOpen.value = false;
    ElMessage.success(`已保存 ${settingsVersion.value} 的启动设置`);
  } catch (error) {
    ElMessage.error(error.message || "保存版本设置失败");
  } finally {
    settingsSubmitting.value = false;
  }
}

async function submitThirdParty() {
  thirdPartySubmitting.value = true;
  try {
    const logged = await mcThirdPartyLogin(
      thirdPartyForm.server.trim(),
      thirdPartyForm.username.trim(),
      thirdPartyForm.password,
    );
    account.value = logged;
    thirdPartyForm.password = "";
    accountDialogOpen.value = false;
    ElMessage.success(`第三方账号 ${logged.name} 登录成功`);
  } catch (error) {
    ElMessage.error(error.message || "第三方登录失败");
  } finally {
    thirdPartySubmitting.value = false;
  }
}

function gameVersionOf(version) {
  if (version.fabric && version.id.startsWith("fabric-loader-")) {
    const index = version.id.lastIndexOf("-");
    return index >= 0 ? version.id.slice(index + 1) : version.id;
  }
  return version.id;
}

function loaderOf(version) {
  return version.fabric ? "fabric" : "";
}

async function openMods(version) {
  modsVersion.value = version.id;
  modsDialogOpen.value = true;
}

async function loadMods() {
  modsLoading.value = true;
  try {
    modsList.value = await mcModsList(modsVersion.value) || [];
  } catch (error) {
    ElMessage.error(error.message || "读取 mod 列表失败");
  } finally {
    modsLoading.value = false;
  }
}

async function toggleMod(mod) {
  try {
    await mcModsToggle(modsVersion.value, mod.filename, !mod.enabled);
    await loadMods();
  } catch (error) {
    ElMessage.error(error.message || "切换 mod 状态失败");
  }
}

async function deleteMod(mod) {
  try {
    await ElMessageBox.confirm(`确定删除 mod「${mod.filename}」？`, "删除 mod", { type: "warning" });
  } catch {
    return;
  }
  try {
    await mcModsDelete(modsVersion.value, mod.filename);
    await loadMods();
    ElMessage.success(`已删除 ${mod.filename}`);
  } catch (error) {
    ElMessage.error(error.message || "删除 mod 失败");
  }
}

async function searchMods() {
  const query = modsSearchQuery.value.trim();
  if (!query) {
    ElMessage.warning("请输入 mod 名称");
    return;
  }
  modsSearching.value = true;
  try {
    const version = installed.value.find((item) => item.id === modsVersion.value) || { id: modsVersion.value, fabric: false };
    modsSearchResults.value = await mcSearchModrinth(query, gameVersionOf(version), loaderOf(version)) || [];
    if (!modsSearchResults.value.length) {
      ElMessage.info("Modrinth 上未找到匹配的 mod");
    }
  } catch (error) {
    ElMessage.error(error.message || "搜索失败");
  } finally {
    modsSearching.value = false;
  }
}

async function installMod(hit) {
  modsInstalling.value = hit.project_id;
  try {
    const version = installed.value.find((item) => item.id === modsVersion.value) || { id: modsVersion.value, fabric: false };
    const filename = await mcModrinthInstall(modsVersion.value, hit.project_id, gameVersionOf(version), loaderOf(version));
    await loadMods();
    ElMessage.success(`已安装 ${hit.title}（${filename}）`);
  } catch (error) {
    ElMessage.error(error.message || "安装 mod 失败");
  } finally {
    modsInstalling.value = "";
  }
}

async function openModsDir() {
  try {
    await mcModsOpenDir(modsVersion.value);
  } catch (error) {
    ElMessage.error(error.message || "打开 mods 目录失败");
  }
}

function formatSize(bytes) {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let index = 0;
  let value = Number(bytes);
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index += 1;
  }
  return `${value.toFixed(value >= 10 || index === 0 ? 0 : 1)} ${units[index]}`;
}

async function openFabric(version) {
  fabricTarget.value = version.id;
  fabricLoader.value = "";
  fabricLoaders.value = [];
  fabricDialogOpen.value = true;
  fabricLoadersLoading.value = true;
  try {
    const loaders = await mcFabricLoaders(version.id);
    fabricLoaders.value = (loaders || []).map((item) => item.loader.version);
    if (fabricLoaders.value.length) fabricLoader.value = fabricLoaders.value[0];
  } catch (error) {
    ElMessage.error(error.message || "拉取 Fabric Loader 列表失败");
  } finally {
    fabricLoadersLoading.value = false;
  }
}

async function submitFabric() {
  if (!fabricLoader.value) {
    ElMessage.warning("请选择 Fabric Loader 版本");
    return;
  }
  await enqueueDownload("fabric", fabricTarget.value, fabricLoader.value);
}

// ---------- 启动 ----------
function versionLabel(version) {
  if (!version) return "";
  return version.fabric ? `${version.id}（Fabric）` : version.id;
}

async function launchGame() {
  if (!hasAccount.value) {
    ElMessage.warning("请先设置国际版账号");
    openAccountDialog();
    return;
  }
  if (!launchForm.version) {
    ElMessage.warning("请选择游戏版本");
    return;
  }
  launching.value = true;
  runningVersion.value = launchForm.version;
  progress.value = null;
  try {
    progress.value = await mcLaunch({
      server_ip: launchForm.server_ip,
      server_port: Number(launchForm.server_port) || 25565,
      instance_dir: launchForm.instance_dir,
      version_id: launchForm.version,
      fabric: launchForm.version.includes("fabric"),
      max_memory_mb: Number(launchForm.max_memory_mb) || 2048,
    });
    startLaunchPolling();
  } catch (error) {
    ElMessage.error(error.message || "启动失败");
    launching.value = false;
  }
}

function startLaunchPolling() {
  if (progressInterval) window.clearInterval(progressInterval);
  progressInterval = window.setInterval(async () => {
    try {
      progress.value = await mcLaunchProgress(runningVersion.value);
      if (progress.value?.status === "done" || progress.value?.status === "failed") {
        stopLaunchPolling();
        if (progress.value?.status === "failed") {
          ElMessage.error(progress.value?.error || "启动失败");
        }
      }
    } catch {
      stopLaunchPolling();
    }
  }, 700);
}

function stopLaunchPolling() {
  if (progressInterval) window.clearInterval(progressInterval);
  progressInterval = 0;
}

async function closeGame() {
  if (!runningVersion.value) return;
  try {
    await mcCloseGame(runningVersion.value);
    ElMessage.success("已结束游戏");
    runningVersion.value = "";
  } catch (error) {
    ElMessage.error(error.message || "结束游戏失败");
  }
}

async function chooseInstanceDir() {
  try {
    const selected = await selectMCGameDirectory();
    if (selected) launchForm.instance_dir = selected;
  } catch (error) {
    ElMessage.error(error.message || "选择实例目录失败");
  }
}

async function chooseJavaPath() {
  try {
    const selected = await selectJavaExecutable();
    if (selected) settingsForm.java_path = selected;
  } catch (error) {
    ElMessage.error(error.message || "选择 Java 路径失败");
  }
}

const progressPercent = computed(() => Math.round(Number(progress.value?.percent || 0)));
const progressStatusText = computed(() => {
  if (!progress.value) return "尚未开始";
  if (progress.value.running) return "游戏已经在运行中";
  if (progress.value.status === "failed") return progress.value.error || "启动失败";
  return progress.value.message || "处理中";
});
</script>

<template>
  <div class="content-stack join-game-page" v-loading="loading">
    <div class="page-toolbar">
      <div>
        <h2>加入游戏</h2>
        <p>国际版 / 离线账号，支持自定义版本下载与可选 Fabric 自动安装</p>
      </div>
      <div class="toolbar-actions">
        <el-button @click="openAccountDialog"><UserRound :size="16" />{{ accountLabel }}</el-button>
        <el-button plain @click="downloadQueueOpen = true"><ListChecks :size="16" />下载队列{{ downloadActiveCount ? ` (${downloadActiveCount})` : "" }}</el-button>
        <el-button type="primary" @click="openAddVersion"><Plus :size="16" />添加版本</el-button>
        <el-button text @click="loadPage"><RefreshCw :size="16" />刷新</el-button>
      </div>
    </div>

    <section class="account-strip" :class="hasAccount ? 'active' : 'disabled'">
      <UserRound :size="18" />
      <div>
        <strong>{{ hasAccount ? "账号已就绪" : "尚未设置账号" }}</strong>
        <span>{{ account?.uuid ? `${account.uuid} · ${modeLabel(account.mode)}` : "点击顶部按钮设置离线名或登录微软/第三方账号" }}</span>
      </div>
      <div v-if="hasAccount" class="account-strip-actions">
        <el-button text type="danger" @click="logoutAccount">退出登录</el-button>
      </div>
    </section>

    <section class="server-list">
      <article v-for="version in installed" :key="version.id" class="server-card">
        <div class="server-card-main">
          <div class="server-symbol"><Package :size="20" /></div>
          <div class="server-copy">
            <h3>{{ versionLabel(version) }}</h3>
            <div class="server-meta">
              <span>版本 ID：{{ version.id }}</span>
              <span>类型：{{ version.fabric ? "Fabric" : "原版" }}</span>
            </div>
          </div>
        </div>
        <div class="server-card-actions">
          <el-button v-if="!version.fabric && !fabricByBase[version.id]" plain @click="openFabric(version)"><Download :size="15" />装 Fabric</el-button>
          <el-tag v-else-if="!version.fabric && fabricByBase[version.id]" size="small" type="success">已装 Fabric</el-tag>
          <el-button plain @click="openMods(version)"><Puzzle :size="15" />Mod</el-button>
          <el-button plain @click="openVersionSettings(version)"><Settings :size="15" />设置</el-button>
          <el-button type="primary" @click="launchForm.version = version.id; launchGame()"><Gamepad2 :size="16" />启动</el-button>
          <el-button type="danger" plain :loading="deletingVersion === version.id" @click="deleteVersion(version)">
            <Trash2 v-if="deletingVersion !== version.id" :size="15" />删除
          </el-button>
        </div>
      </article>
      <div v-if="!installed.length" class="join-empty">
        <Package :size="28" />
        <strong>暂无已安装版本</strong>
        <span>点击右上角“添加版本”从 Mojang 下载，或选择离线账号后直接启动</span>
      </div>
    </section>

    <section class="launch-form-frame">
      <h3>快速启动</h3>
      <el-form label-position="top" @submit.prevent="launchGame">
        <div class="dialog-form-grid">
          <el-form-item label="游戏版本">
            <el-select v-model="launchForm.version" placeholder="选择已安装版本">
              <el-option v-for="version in installed" :key="version.id" :label="versionLabel(version)" :value="version.id" />
            </el-select>
          </el-form-item>
          <el-form-item label="服务器 IP"><el-input v-model="launchForm.server_ip" /></el-form-item>
          <el-form-item label="端口"><el-input-number v-model="launchForm.server_port" :min="1" :max="65535" /></el-form-item>
          <el-form-item label="最大内存 (MB)"><el-input-number v-model="launchForm.max_memory_mb" :min="512" :step="512" /></el-form-item>
          <el-form-item label="实例目录（可选，合并 mods/config 等）" class="full-width-form-item">
            <div class="path-input-row">
              <el-input v-model="launchForm.instance_dir" placeholder="包含 mods/config/resourcepacks 的目录" clearable />
              <el-button @click="chooseInstanceDir"><FolderOpen :size="16" />浏览</el-button>
            </div>
          </el-form-item>
        </div>
        <div class="launch-actions">
          <el-button v-if="runningVersion" type="danger" plain @click="closeGame"><Square :size="15" />结束游戏</el-button>
          <el-button type="primary" :loading="launching" @click="launchGame"><Gamepad2 :size="15" />启动游戏</el-button>
        </div>
      </el-form>
      <div v-if="progress && (progress.status === 'done' || progress.status === 'failed')" class="launch-result" :class="progress.status">
        <span>{{ progress.running ? "游戏正在运行" : progress.status === 'done' ? '启动成功' : '启动失败' }}</span>
        <pre v-if="progress.error">{{ progress.error }}</pre>
      </div>
    </section>
  </div>

  <el-dialog v-model="accountDialogOpen" title="国际版账号" width="480px">
    <el-tabs>
      <el-tab-pane label="第三方认证">
        <el-form label-position="top" @submit.prevent="submitThirdParty">
          <el-form-item label="认证服务器地址（authlib-injector，如 LittleSkin）">
            <el-input v-model="thirdPartyForm.server" placeholder="https://littleskin.cn/api/yggdrasil" clearable />
          </el-form-item>
          <el-form-item label="账号">
            <el-input v-model="thirdPartyForm.username" placeholder="第三方平台账号" autocomplete="username" />
          </el-form-item>
          <el-form-item label="密码">
            <el-input v-model="thirdPartyForm.password" type="password" show-password placeholder="第三方平台密码" autocomplete="current-password" @keyup.enter="submitThirdParty" />
          </el-form-item>
        </el-form>
        <div class="dialog-footer-inline">
          <el-button type="primary" :loading="thirdPartySubmitting" @click="submitThirdParty"><UserRound :size="15" />登录</el-button>
        </div>
        <p class="muted-tip">登录后启动时会自动注入 authlib-injector 代理第三方认证（首次自动下载）。</p>
      </el-tab-pane>
      <el-tab-pane label="离线账号">
        <el-form label-position="top" @submit.prevent="submitOffline">
          <el-form-item label="玩家名"><el-input v-model="offlineName" maxlength="16" placeholder="离线模式下使用的玩家名" /></el-form-item>
        </el-form>
        <div class="dialog-footer-inline">
          <el-button type="primary" @click="submitOffline">设为离线账号</el-button>
        </div>
      </el-tab-pane>
      <el-tab-pane label="微软账号">
        <template v-if="!deviceLogin">
          <p class="muted-tip">使用微软设备码登录，可连接在线模式的国际版服务器。需要服务器能访问微软与 Mojang 接口。</p>
          <el-button type="primary" @click="startMicrosoftLogin"><UserRound :size="15" />开始微软登录</el-button>
        </template>
        <template v-else>
          <div class="device-login">
            <p>请在浏览器打开：<code>{{ deviceLogin.verification_uri }}</code></p>
            <p>并输入代码：<strong class="device-code">{{ deviceLogin.user_code }}</strong></p>
            <el-button :loading="devicePolling" @click="stopDevicePolling">停止等待</el-button>
          </div>
        </template>
      </el-tab-pane>
    </el-tabs>
  </el-dialog>

  <el-dialog v-model="addVersionDialogOpen" title="添加版本" width="520px">
    <el-form label-position="top" @submit.prevent="submitAddVersion">
      <el-form-item label="游戏版本（从 Mojang 下载 client / libraries / assets）">
        <el-select
          v-model="addVersionID"
          filterable
          allow-create
          default-first-option
          placeholder="输入或选择版本，如 1.21.4；或粘贴版本 JSON 直链"
          :loading="availableLoading"
        >
          <el-option v-for="version in available" :key="version.id" :label="version.id" :value="version.id" />
        </el-select>
      </el-form-item>
      <el-form-item label="提示">
        <p class="muted-tip">点击「下载安装」后任务加入下载队列，可同时下载多个版本。</p>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="addVersionDialogOpen = false">取消</el-button>
      <el-button type="primary" @click="submitAddVersion"><Download :size="15" />加入下载队列</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="settingsDialogOpen" :title="`${settingsVersion} 启动设置`" width="560px">
    <el-form label-position="top">
      <div class="dialog-form-grid">
        <el-form-item label="服务器 IP"><el-input v-model="settingsForm.server_ip" /></el-form-item>
        <el-form-item label="端口"><el-input-number v-model="settingsForm.server_port" :min="1" :max="65535" /></el-form-item>
        <el-form-item label="最大内存 (MB)"><el-input-number v-model="settingsForm.max_memory_mb" :min="512" :step="512" /></el-form-item>
        <el-form-item label="实例目录（可选，合并 mods/config 等）">
          <div class="path-input-row">
            <el-input v-model="settingsForm.instance_dir" placeholder="该版本独立的 mods 目录位置" clearable />
            <el-button @click="chooseInstanceDir"><FolderOpen :size="16" />浏览</el-button>
          </div>
        </el-form-item>
        <el-form-item label="额外 JVM 参数（可选）" class="full-width-form-item">
          <el-input v-model="settingsForm.jvm_args" placeholder="例如 -XX:+UseG1GC -Dfml.ignoreInvalidMinecraftCertificates=true" />
        </el-form-item>
        <el-form-item label="Java 路径（可选，留空自动选择）" class="full-width-form-item">
          <div class="path-input-row">
            <el-input v-model="settingsForm.java_path" placeholder="java.exe 完整路径，如 C:\Java\jdk-21\bin\java.exe" clearable />
            <el-button @click="chooseJavaPath"><FolderOpen :size="16" />浏览</el-button>
          </div>
        </el-form-item>
        <el-form-item label="窗口分辨率（可选，0 = 不设置）" class="full-width-form-item">
          <div class="dialog-form-grid resolution-grid">
            <el-form-item label="宽"><el-input-number v-model="settingsForm.width" :min="0" :max="7680" :step="80" /></el-form-item>
            <el-form-item label="高"><el-input-number v-model="settingsForm.height" :min="0" :max="4320" :step="80" /></el-form-item>
          </div>
        </el-form-item>
        <el-form-item label="启动版本" class="full-width-form-item">
          <el-radio-group v-model="settingsForm.launch_version" class="launch-version-options">
            <el-radio value="">默认（该版本自身）</el-radio>
            <el-radio v-for="option in launchVersionOptions" :key="option.id" :value="option.id">{{ versionLabel(option) }}</el-radio>
          </el-radio-group>
          <p v-if="!launchVersionOptions.length" class="muted-tip">该版本未安装 Fabric；如需 Fabric 启动，先点“装 Fabric”。</p>
          <p v-else class="muted-tip">选择该版本对应的 Fabric 子版本作为启动版本，mods 与配置与当前版本共用。</p>
        </el-form-item>
        <el-form-item label="自动使用 Fabric" class="full-width-form-item">
          <el-switch v-model="settingsForm.use_fabric" :disabled="!launchVersionOptions.length" />
          <p class="muted-tip">开启后，若该版本已装 Fabric，启动时自动用其 Fabric 子版本（显式“启动版本”优先）。</p>
        </el-form-item>
      </div>
    </el-form>
    <template #footer>
      <el-button @click="settingsDialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="settingsSubmitting" @click="saveVersionSettings">保存设置</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="modsDialogOpen" :title="`${modsVersion} · Mod 管理`" width="720px" @open="loadMods">
      <div class="mods-toolbar">
      <el-input v-model="modsSearchQuery" placeholder="在 Modrinth 搜索 mod（例如 sodium）" clearable @keyup.enter="searchMods">
        <template #append>
          <el-button :loading="modsSearching" @click="searchMods"><Search :size="15" />搜索</el-button>
        </template>
      </el-input>
      <el-button plain @click="openModsDir"><FolderOpen :size="15" />打开目录</el-button>
    </div>

    <div v-if="modsSearchResults.length" class="mods-search-results">
      <p class="mods-section-title">Modrinth 搜索结果（点击安装最新兼容版本）</p>
      <div v-for="hit in modsSearchResults" :key="hit.project_id" class="modr-hit">
        <div class="modr-hit-copy">
          <strong>{{ hit.title }}</strong>
          <span>{{ hit.author }} · {{ hit.description }}</span>
        </div>
        <el-button size="small" type="primary" :loading="modsInstalling === hit.project_id" @click="installMod(hit)"><Download :size="14" />安装</el-button>
      </div>
    </div>

    <el-divider v-if="modsSearchResults.length" />

    <div v-loading="modsLoading" class="mods-list">
      <p class="mods-section-title">已安装（{{ modsList.length }}）</p>
      <div v-if="!modsLoading && !modsList.length" class="mods-empty">
        <Puzzle :size="22" />
        <span>该版本还没有安装任何 mod，可点击上方“搜索”从 Modrinth 安装。</span>
      </div>
      <div v-for="mod in modsList" :key="mod.filename" class="mod-row" :class="{ disabled: !mod.enabled }">
        <div class="mod-row-copy">
          <strong>{{ mod.filename }}</strong>
          <span>{{ mod.enabled ? "已启用" : "已禁用" }} · {{ formatSize(mod.size) }}</span>
        </div>
        <div class="mod-row-actions">
          <el-switch :model-value="mod.enabled" @change="toggleMod(mod)" />
          <el-button text type="danger" size="small" @click="deleteMod(mod)"><Trash2 :size="15" /></el-button>
        </div>
      </div>
    </div>
    <template #footer>
      <el-button @click="modsDialogOpen = false">关闭</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="fabricDialogOpen" :title="`为 ${fabricTarget} 安装 Fabric`" width="520px">
    <el-form label-position="top">
      <el-form-item label="Fabric Loader 版本">
        <el-select v-model="fabricLoader" :loading="fabricLoadersLoading" placeholder="选择 Loader 版本">
          <el-option v-for="loader in fabricLoaders" :key="loader" :label="loader" :value="loader" />
        </el-select>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="fabricDialogOpen = false">取消</el-button>
      <el-button type="primary" @click="submitFabric">加入下载队列</el-button>
    </template>
  </el-dialog>

  <el-drawer v-model="downloadQueueOpen" title="下载队列" size="480px">
    <div class="queue-toolbar">
      <span class="muted-tip">最多同时下载 3 个版本，其余自动排队。</span>
      <el-button size="small" plain :disabled="!downloadTasks.some((t) => ['done', 'failed', 'canceled'].includes(t.status))" @click="clearFinishedDownloads">清空已完成</el-button>
    </div>
    <div v-if="!downloadTasks.length" class="queue-empty">
      <ListChecks :size="26" />
      <span>队列为空，点击「添加版本」或版本卡片「装 Fabric」加入下载。</span>
    </div>
    <div v-for="task in downloadTasks" :key="task.id" class="queue-task" :class="task.status">
      <div class="queue-task-head">
        <div class="queue-task-title">
          <strong>{{ task.kind === "fabric" ? `Fabric ${task.version_id} + ${task.loader}` : task.version_id }}</strong>
          <span class="queue-status" :class="task.status">{{ queueStatusLabel(task) }}</span>
        </div>
        <el-button v-if="task.status === 'queued' || task.status === 'downloading'" size="small" text type="danger" @click="cancelDownload(task)">取消</el-button>
        <el-button v-else size="small" text @click="removeDownload(task)">移除</el-button>
      </div>
      <el-progress
        :percentage="Math.round(task.percent)"
        :status="task.status === 'failed' ? 'exception' : task.status === 'done' ? 'success' : undefined"
      />
      <p class="queue-message">
        {{ task.stage === "assets" || task.stage === "download" || task.stage === "prepare" ? task.message : `${task.stage} · ${task.message}` }}
        <span v-if="task.status === 'failed' && task.error" class="queue-error">{{ task.error }}</span>
      </p>
    </div>
  </el-drawer>
</template>

<style scoped>
.join-game-page { min-width: 0; }
.account-strip { display: flex; align-items: center; gap: 12px; padding: 13px 15px; background: var(--app-surface); border: 1px solid var(--app-border); border-radius: 6px; }
.account-strip.active { color: var(--el-color-success); background: var(--app-surface-muted); }
.account-strip.disabled { color: var(--el-color-error); background: var(--app-surface-muted); }
.account-strip strong, .account-strip span { display: block; }
.account-strip span { margin-top: 3px; color: var(--app-text-secondary); font-size: 12px; }
.account-strip-actions { display: flex; align-items: center; gap: 8px; margin-left: auto; }
.account-strip-actions .el-button { margin-left: 0; }
.server-list { display: grid; gap: 12px; }
.server-card { display: flex; align-items: stretch; justify-content: space-between; gap: 16px; padding: 15px; background: var(--app-surface); border: 1px solid var(--app-border); border-radius: 6px; }
.server-card-main { display: flex; gap: 13px; min-width: 0; }
.server-symbol { display: grid; place-items: center; width: 42px; height: 42px; color: var(--el-color-primary); background: var(--app-primary-soft); border-radius: 6px; }
.server-copy { min-width: 0; }
.server-copy h3 { margin: 0; font-size: 16px; }
.server-meta { display: flex; flex-wrap: wrap; gap: 8px 14px; margin-top: 10px; color: var(--app-text-muted); font-size: 12px; }
.server-card-actions { display: flex; align-items: center; gap: 8px; flex: 0 0 auto; }
.join-empty { display: grid; place-items: center; gap: 8px; min-height: 180px; color: var(--app-text-muted); background: var(--app-surface); border: 1px dashed var(--app-border); border-radius: 6px; text-align: center; }
.launch-form-frame { margin-top: 16px; padding: 18px; background: var(--app-surface); border: 1px solid var(--app-border); border-radius: 6px; }
.launch-form-frame h3 { margin: 0 0 14px; font-size: 15px; }
.dialog-form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.full-width-form-item { grid-column: 1 / -1; }
.path-input-row { display: flex; width: 100%; gap: 8px; }
.path-input-row .el-input { flex: 1; }
.launch-actions { display: flex; justify-content: flex-end; gap: 8px; }
.launch-result { margin-top: 14px; padding: 10px 12px; border: 1px solid var(--app-border); border-radius: 6px; font-size: 13px; }
.launch-result.done { color: var(--el-color-success); background: var(--app-surface-muted); }
.launch-result.failed { color: var(--el-color-error); background: var(--app-surface-muted); }
.launch-result pre { margin: 8px 0 0; white-space: pre-wrap; }
.dialog-footer-inline { display: flex; justify-content: flex-end; }
.muted-tip { color: var(--app-text-secondary); font-size: 13px; }
.device-login { display: grid; gap: 10px; }
.device-code { font-size: 20px; letter-spacing: 2px; }
.install-progress p { margin: 8px 0 0; color: var(--app-text-secondary); font-size: 12px; }
.mods-toolbar { display: flex; gap: 8px; }
.mods-toolbar .el-input { flex: 1; }
.mods-section-title { margin: 4px 0 8px; font-size: 12px; color: var(--app-text-secondary); }
.mods-search-results { display: grid; gap: 8px; }
.modr-hit { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 12px; border: 1px solid var(--app-border); border-radius: 6px; background: var(--app-surface-muted); }
.modr-hit-copy { min-width: 0; }
.modr-hit-copy strong, .modr-hit-copy span { display: block; }
.modr-hit-copy span { margin-top: 3px; color: var(--app-text-secondary); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mods-list { display: grid; gap: 6px; }
.mod-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 8px 12px; border: 1px solid var(--app-border); border-radius: 6px; }
.mod-row.disabled { opacity: 0.55; }
.mod-row-copy { min-width: 0; }
.mod-row-copy strong, .mod-row-copy span { display: block; }
.mod-row-copy strong { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mod-row-copy span { margin-top: 2px; color: var(--app-text-secondary); font-size: 12px; }
.mod-row-actions { display: flex; align-items: center; gap: 6px; flex: 0 0 auto; }
.mods-empty { display: grid; place-items: center; gap: 8px; padding: 24px; color: var(--app-text-muted); border: 1px dashed var(--app-border); border-radius: 6px; text-align: center; }
.resolution-grid { grid-template-columns: 1fr 1fr; gap: 10px; }
.launch-version-options { display: flex; flex-direction: column; align-items: flex-start; gap: 4px; }
.queue-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 12px; }
.queue-empty { display: grid; place-items: center; gap: 10px; padding: 32px 0; color: var(--app-text-muted); text-align: center; }
.queue-task { display: grid; gap: 8px; padding: 12px; margin-bottom: 10px; border: 1px solid var(--app-border); border-radius: 6px; background: var(--app-surface-muted); }
.queue-task.failed { border-color: var(--el-color-danger); }
.queue-task.done { border-color: var(--el-color-success); }
.queue-task-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.queue-task-title { display: flex; align-items: center; gap: 8px; min-width: 0; }
.queue-task-title strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.queue-status { flex: 0 0 auto; font-size: 12px; color: var(--app-text-secondary); }
.queue-status.downloading { color: var(--el-color-primary); }
.queue-status.done { color: var(--el-color-success); }
.queue-status.failed { color: var(--el-color-danger); }
.queue-status.canceled { color: var(--app-text-muted); }
.queue-message { margin: 0; color: var(--app-text-secondary); font-size: 12px; overflow-wrap: anywhere; }
.queue-error { color: var(--el-color-danger); }
@media (max-width: 900px) {
  .dialog-form-grid { grid-template-columns: 1fr; }
  .server-card { flex-direction: column; }
  .server-card-actions { justify-content: flex-end; }
  .account-strip { align-items: flex-start; flex-wrap: wrap; }
}
</style>
