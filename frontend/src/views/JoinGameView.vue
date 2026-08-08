<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { FolderOpen, Gamepad2, Plus, RefreshCw, Server, Settings, ShieldCheck, Trash2, UserRound } from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  createGameServer,
  deleteGameServer,
  deleteNetEaseAccount,
  deleteNetGameCharacter,
  gameJoinProgress,
  gameServerRunning,
  gameServers,
  isWinApp,
  joinGameServer,
  loginNetEaseAccount,
  netGameLaunchOptions,
  netEaseAccount,
  selectGameModDirectory,
  updateGameServer,
} from "../runtime";

const loading = ref(false);
const deletingID = ref("");
const deletingCharacter = ref("");
const editingServerID = ref("");
const loginDialogOpen = ref(false);
const serverDialogOpen = ref(false);
const progressDialogOpen = ref(false);
const loginSubmitting = ref(false);
const serverSubmitting = ref(false);
const account = ref(null);
const servers = ref([]);
const characters = ref([]);
const characterLoading = ref(false);
const activeServer = ref(null);
const progress = ref(null);
const loginForm = reactive({ email: "", password: "" });
const serverForm = reactive(defaultServerForm());
let progressTimer = 0;

const hasNetEaseAccount = computed(() => Boolean(account.value?.email));
const characterOptions = computed(() => characters.value.map((item) => item.name).filter(Boolean));
const canCreateCharacter = computed(() => characters.value.length < 3);
const serverDialogTitle = computed(() => editingServerID.value ? "配置服务器" : "新增服务器配置");
const progressPercent = computed(() => Math.round(Number(progress.value?.percent || 0)));
const progressStatusText = computed(() => {
  if (!progress.value) return "尚未开始";
  if (progress.value.running) return "游戏已经在运行中";
  if (progress.value.status === "failed") return progress.value.error || "加入准备失败";
  return progress.value.message || "处理中";
});

onMounted(loadPage);
onBeforeUnmount(stopProgressPolling);

watch(progressDialogOpen, (open) => {
  if (!open) stopProgressPolling();
});

function defaultServerForm() {
  return { name: "", game_id: "", ip: "127.0.0.1", port: 25565, username: "", version: "", mod_dir: "" };
}

async function loadPage() {
  loading.value = true;
  try {
    const [accountResult, serverResult] = await Promise.all([
      isWinApp() ? netEaseAccount() : Promise.resolve(null),
      isWinApp() ? gameServers() : Promise.resolve([]),
    ]);
    account.value = accountResult || null;
    servers.value = serverResult || [];
    if (account.value?.email) loginForm.email = account.value.email;
  } catch (error) {
    ElMessage.error(error.message || "加载加入游戏配置失败");
  } finally {
    loading.value = false;
  }
}

function openLoginDialog() {
  if (account.value?.email) loginForm.email = account.value.email;
  loginForm.password = "";
  loginDialogOpen.value = true;
}

async function submitLogin() {
  if (!requireValue(loginForm.email, "请输入网易邮箱") || !requireValue(loginForm.password, "请输入网易密码")) return;
  loginSubmitting.value = true;
  try {
    account.value = await loginNetEaseAccount(loginForm.email, loginForm.password);
    loginForm.password = "";
    loginDialogOpen.value = false;
    ElMessage.success("网易账号登录成功");
  } catch (error) {
    ElMessage.error(error.message || "网易账号登录失败");
  } finally {
    loginSubmitting.value = false;
  }
}

async function removeNetEaseAccount() {
  if (!hasNetEaseAccount.value) return;
  try {
    await ElMessageBox.confirm(`删除本地保存的网易账号：${account.value.email}？`, "删除网易账号", {
      type: "warning", confirmButtonText: "删除", cancelButtonText: "取消",
    });
    await deleteNetEaseAccount();
    account.value = null;
    ElMessage.success("本地网易账号已删除");
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message || "删除失败");
  }
}

function openServerDialog() {
  if (!hasNetEaseAccount.value) {
    ElMessage.warning("请先登录网易账号");
    openLoginDialog();
    return;
  }
  editingServerID.value = "";
  Object.assign(serverForm, defaultServerForm());
  characters.value = [];
  serverDialogOpen.value = true;
}

function openServerSettings(server) {
  editingServerID.value = server.id;
  Object.assign(serverForm, {
    name: server.name || "",
    game_id: server.game_id || "",
    ip: server.ip || "127.0.0.1",
    port: Number(server.port) || 25565,
    username: server.username || "",
    version: server.version_label || "",
    mod_dir: server.mod_dir || "",
  });
  characters.value = [];
  serverDialogOpen.value = true;
  loadServerGameOptions();
}

async function loadServerGameOptions() {
  if (!serverForm.game_id.trim() || !isWinApp()) return;
  characterLoading.value = true;
  try {
    const options = await netGameLaunchOptions(serverForm.game_id.trim());
    serverForm.game_id = options.detail.game_id;
    serverForm.version = options.detail.version_label;
    characters.value = options.characters || [];
    if (!serverForm.username && characterOptions.value.length) serverForm.username = characterOptions.value[0];
  } catch (error) {
    characters.value = [];
    ElMessage.error(error.message || "加载网络游戏角色失败");
  } finally {
    characterLoading.value = false;
  }
}

async function chooseServerResourceDirectory() {
  try {
    const selected = await selectGameModDirectory();
    if (selected) serverForm.mod_dir = selected;
  } catch (error) {
    ElMessage.error(error.message || "选择资源目录失败");
  }
}

async function removeCharacter(roleName) {
  if (!serverForm.game_id || !roleName || deletingCharacter.value) return;
  try {
    await ElMessageBox.confirm(`删除网易角色：${roleName}？`, "删除角色", {
      type: "warning", confirmButtonText: "删除", cancelButtonText: "取消",
    });
    deletingCharacter.value = roleName;
    characters.value = await deleteNetGameCharacter(serverForm.game_id, roleName);
    if (serverForm.username === roleName) serverForm.username = characterOptions.value[0] || "";
    ElMessage.success("角色已删除");
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message || "删除角色失败");
  } finally {
    deletingCharacter.value = "";
  }
}

async function submitServer() {
  if (!validateServerForm()) return;
  serverSubmitting.value = true;
  try {
    const input = {
      name: serverForm.name,
      game_id: serverForm.game_id,
      ip: serverForm.ip || "127.0.0.1",
      port: Number(serverForm.port) || 25565,
      username: serverForm.username,
      mod_dir: serverForm.mod_dir,
    };
    if (editingServerID.value) {
      const updated = await updateGameServer(editingServerID.value, input);
      servers.value = servers.value.map((server) => server.id === updated.id ? updated : server);
      ElMessage.success("服务器配置已保存");
    } else {
      const created = await createGameServer(input);
      servers.value = [created, ...servers.value];
      ElMessage.success("服务器配置已创建");
    }
    serverDialogOpen.value = false;
  } catch (error) {
    ElMessage.error(error.message || "保存服务器配置失败");
  } finally {
    serverSubmitting.value = false;
  }
}

async function joinServer(server) {
  if (!hasNetEaseAccount.value) {
    ElMessage.warning("请先登录网易账号");
    openLoginDialog();
    return;
  }
  activeServer.value = server;
  progressDialogOpen.value = true;
  stopProgressPolling();
  try {
    if (await gameServerRunning(server.id)) {
      progress.value = { server_id: server.id, status: "done", message: "游戏已经在运行中", percent: 100, running: true };
      ElMessage.info("游戏已经在运行中了");
      return;
    }
    progress.value = await joinGameServer(server.id);
    startProgressPolling(server.id);
  } catch (error) {
    progress.value = { server_id: server.id, status: "failed", message: "加入准备失败", percent: 0, error: error.message || "未知错误" };
    ElMessage.error(error.message || "加入失败");
  }
}

function startProgressPolling(serverID) {
  stopProgressPolling();
  progressTimer = window.setInterval(async () => {
    try {
      progress.value = await gameJoinProgress(serverID);
      if (progress.value?.status === "done") {
        stopProgressPolling();
        window.setTimeout(() => { progressDialogOpen.value = false; }, 600);
      } else if (progress.value?.status === "failed") {
        stopProgressPolling();
      }
    } catch (error) {
      stopProgressPolling();
      progress.value = { server_id: serverID, status: "failed", message: "读取进度失败", percent: 0, error: error.message || "未知错误" };
    }
  }, 600);
}

function stopProgressPolling() {
  if (progressTimer) window.clearInterval(progressTimer);
  progressTimer = 0;
}

async function removeServer(server) {
  deletingID.value = server.id;
  try {
    await ElMessageBox.confirm(`删除服务器配置：${server.name}？配置删除后不能恢复。`, "删除服务器配置", {
      type: "warning", confirmButtonText: "删除", cancelButtonText: "取消",
    });
    servers.value = await deleteGameServer(server.id);
    ElMessage.success("服务器配置已删除");
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message || "删除服务器配置失败");
  } finally {
    deletingID.value = "";
  }
}

function validateServerForm() {
  const port = Number(serverForm.port);
  if (!requireValue(serverForm.name, "请输入配置名称")) return false;
  if (!requireValue(serverForm.game_id, "请输入网络游戏 ID")) return false;
  if (!requireValue(serverForm.ip, "请输入服务器 IP")) return false;
  if (!requireValue(serverForm.username, "请输入角色用户名")) return false;
  if (!requireValue(serverForm.mod_dir, "请选择自定义资源目录")) return false;
  if (port < 1 || port > 65535) {
    ElMessage.warning("端口必须在 1-65535 之间");
    return false;
  }
  return true;
}

function requireValue(value, message) {
  if (String(value || "").trim()) return true;
  ElMessage.warning(message);
  return false;
}

function formatTime(value) {
  if (!value) return "--";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
  }).format(new Date(value));
}
</script>

<template>
  <div class="content-stack join-game-page" v-loading="loading">
    <div class="page-toolbar">
      <div>
        <h2>加入游戏</h2>
        <p>服务器配置可随时修改，启动前会合并所选自定义资源目录</p>
      </div>
      <div class="toolbar-actions join-toolbar-actions">
        <el-button @click="openLoginDialog"><UserRound :size="16" />{{ hasNetEaseAccount ? account.email : "登录网易账号" }}</el-button>
        <el-button type="primary" @click="openServerDialog"><Plus :size="16" />新增服务器</el-button>
        <el-button text @click="loadPage"><RefreshCw :size="16" />刷新</el-button>
      </div>
    </div>

    <section class="account-strip" :class="hasNetEaseAccount ? 'active' : 'disabled'">
      <ShieldCheck :size="18" />
      <div>
        <strong>{{ hasNetEaseAccount ? "网易账号已登录" : "未登录网易账号" }}</strong>
        <span>{{ hasNetEaseAccount ? account.email : "点击顶部按钮登录后才能加入服务器" }}</span>
      </div>
      <el-button v-if="hasNetEaseAccount" text type="danger" @click="removeNetEaseAccount">删除本地账号</el-button>
    </section>

    <section class="server-list">
      <article v-for="server in servers" :key="server.id" class="server-card">
        <div class="server-card-main">
          <div class="server-symbol"><Server :size="20" /></div>
          <div class="server-copy">
            <h3>{{ server.name }}</h3>
            <p>{{ server.ip }}:{{ server.port }}</p>
            <div class="server-meta">
              <span>游戏 ID：{{ server.game_id }}</span>
              <span>角色：{{ server.username }}</span>
              <span>版本：{{ server.version_label }}</span>
              <span>资源：{{ server.mod_dir }}</span>
              <span>创建：{{ formatTime(server.created_at) }}</span>
            </div>
          </div>
        </div>
        <div class="server-card-actions">
          <el-button type="primary" @click="joinServer(server)"><Gamepad2 :size="16" />加入</el-button>
          <el-button @click="openServerSettings(server)"><Settings :size="16" />配置</el-button>
          <el-button type="danger" plain :loading="deletingID === server.id" @click="removeServer(server)">
            <Trash2 v-if="deletingID !== server.id" :size="16" />删除
          </el-button>
        </div>
      </article>
      <div v-if="!servers.length" class="join-empty">
        <Server :size="28" />
        <strong>暂无服务器配置</strong>
        <span>点击右上角“新增服务器”创建一个固定配置</span>
      </div>
    </section>
  </div>

  <el-dialog v-model="progressDialogOpen" :title="activeServer ? `启动进度 - ${activeServer.name}` : '启动进度'" width="520px" :close-on-click-modal="false">
    <div class="launch-progress-body">
      <el-progress :percentage="progressPercent" :status="progress?.status === 'failed' ? 'exception' : progress?.status === 'done' ? 'success' : undefined" />
      <div class="launch-progress-message">
        <strong>{{ progressStatusText }}</strong>
        <span v-if="progress?.stage">阶段：{{ progress.stage }}</span>
      </div>
      <pre v-if="progress?.error" class="launch-error">{{ progress.error }}</pre>
    </div>
    <template #footer>
      <el-button @click="progressDialogOpen = false">关闭</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="loginDialogOpen" title="登录网易账号" width="460px">
    <el-form label-position="top" @submit.prevent="submitLogin">
      <el-form-item label="网易邮箱"><el-input v-model="loginForm.email" autocomplete="username" /></el-form-item>
      <el-form-item label="网易密码"><el-input v-model="loginForm.password" type="password" show-password autocomplete="current-password" /></el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="loginDialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="loginSubmitting" @click="submitLogin">登录</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="serverDialogOpen" :title="serverDialogTitle" width="640px">
    <el-form label-position="top" @submit.prevent="submitServer">
      <div class="dialog-form-grid">
        <el-form-item label="配置名称"><el-input v-model="serverForm.name" placeholder="例如 本地测试服" /></el-form-item>
        <el-form-item label="网络游戏 ID">
          <el-input v-model="serverForm.game_id" placeholder="46 开头的网易网络游戏 ID" @blur="loadServerGameOptions" />
        </el-form-item>
        <el-form-item label="服务器 IP"><el-input v-model="serverForm.ip" /></el-form-item>
        <el-form-item label="端口"><el-input-number v-model="serverForm.port" :min="1" :max="65535" /></el-form-item>
        <el-form-item label="角色用户名">
          <el-select
            v-model="serverForm.username"
            filterable
            :allow-create="canCreateCharacter"
            default-first-option
            :loading="characterLoading"
            :placeholder="canCreateCharacter ? '输入或选择网易已有角色' : '角色已达 3 个，请先删除一个角色'"
          >
            <el-option v-for="character in characters" :key="character.name" :label="character.name" :value="character.name">
              <div class="character-option">
                <span>{{ character.name }}</span>
                <el-button link type="danger" :loading="deletingCharacter === character.name" @mousedown.stop @click.stop.prevent="removeCharacter(character.name)">
                  <Trash2 v-if="deletingCharacter !== character.name" :size="14" />
                </el-button>
              </div>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item label="网易游戏版本"><el-input :model-value="serverForm.version ? String(serverForm.version) : '输入游戏 ID 后自动获取'" disabled /></el-form-item>
        <el-form-item label="自定义资源目录" class="full-width-form-item">
          <div class="path-input-row">
            <el-input v-model="serverForm.mod_dir" placeholder="包含 mods/config/resourcepacks/shaderpacks 的目录" />
            <el-button @click="chooseServerResourceDirectory"><FolderOpen :size="16" />浏览</el-button>
          </div>
        </el-form-item>
      </div>
    </el-form>
    <template #footer>
      <el-button @click="serverDialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="serverSubmitting" @click="submitServer">{{ editingServerID ? "保存配置" : "创建服务器" }}</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.join-game-page { min-width: 0; }
.join-toolbar-actions { flex-wrap: wrap; }
.account-strip { display: flex; align-items: center; gap: 12px; padding: 13px 15px; background: #fff; border: 1px solid #dce2dd; border-radius: 6px; }
.account-strip.active { color: #1f6546; background: #f4faf6; }
.account-strip.disabled { color: #884c43; background: #fff7f5; }
.account-strip strong, .account-strip span { display: block; }
.account-strip span { margin-top: 3px; color: #68746d; font-size: 12px; }
.account-strip .el-button { margin-left: auto; }
.server-list { display: grid; gap: 12px; }
.server-card { display: flex; align-items: stretch; justify-content: space-between; gap: 16px; padding: 15px; background: #fff; border: 1px solid #dce2dd; border-radius: 6px; }
.server-card-main { display: flex; gap: 13px; min-width: 0; }
.server-symbol { display: grid; place-items: center; width: 42px; height: 42px; color: #2563a8; background: #edf3f8; border-radius: 6px; }
.server-copy { min-width: 0; }
.server-copy h3 { margin: 0; font-size: 16px; }
.server-copy p { margin: 4px 0 0; color: #536057; font-weight: 600; }
.server-meta { display: flex; flex-wrap: wrap; gap: 8px 14px; margin-top: 10px; color: #6e7871; font-size: 12px; }
.server-meta span { word-break: break-all; }
.server-card-actions { display: flex; align-items: center; gap: 8px; flex: 0 0 auto; }
.join-empty { display: grid; place-items: center; gap: 8px; min-height: 180px; color: #748079; background: #fff; border: 1px dashed #d5ddd7; border-radius: 6px; text-align: center; }
.join-empty strong, .join-empty span { display: block; }
.path-input-row { display: flex; width: 100%; gap: 8px; }
.path-input-row .el-input { flex: 1; }
.full-width-form-item { grid-column: 1 / -1; }
.character-option { display: flex; align-items: center; justify-content: space-between; width: 100%; gap: 8px; }
.character-option .el-button { margin: 0; }
.launch-progress-body { display: grid; gap: 14px; }
.launch-progress-message strong, .launch-progress-message span { display: block; }
.launch-progress-message span { margin-top: 5px; color: #6e7871; font-size: 12px; }
.launch-error { overflow: auto; margin: 0; padding: 10px; color: #8b2d24; background: #fff4f2; border: 1px solid #f2d0ca; border-radius: 6px; white-space: pre-wrap; }
@media (max-width: 900px) { .server-card { flex-direction: column; } .server-card-actions { justify-content: flex-end; } .path-input-row { flex-direction: column; } }
</style>
