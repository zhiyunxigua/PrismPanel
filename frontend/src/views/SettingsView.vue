<script setup>
import { onBeforeUnmount, onMounted, reactive, ref } from "vue";
import { Bug, FolderOpen, RefreshCw, Search, Settings, Trash2 } from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  mcDevLogClear,
  mcDevLogList,
  mcDevLogPath,
  mcDevModeEnabled,
  mcGetLauncherSettings,
  mcOpenDevLog,
  mcSaveLauncherSettings,
  mcSetDevMode,
  selectJavaExecutable,
  selectMCGameDirectory,
  setDevLogging,
} from "../runtime";

const loading = ref(false);
const saving = ref(false);
const devMode = ref(false);
const devLogDialogOpen = ref(false);
const devLogEntries = ref([]);
const devLogLoading = ref(false);
const devLogPath = ref("");
const devLogListener = ref(null);

const launcherSettings = reactive({ concurrency: 8, mirror: "auto", game_dir: "", default_java: "", default_memory_mb: 2048 });
const customMirror = ref("");

onMounted(loadPage);
onBeforeUnmount(() => {
  if (devLogListener.value) {
    if (window.runtime && typeof window.runtime.EventsOff === "function") {
      window.runtime.EventsOff("prism:dev-log", devLogListener.value);
    }
    devLogListener.value = null;
  }
});

async function loadPage() {
  loading.value = true;
  try {
    const settings = await mcGetLauncherSettings();
    launcherSettings.concurrency = Number(settings?.concurrency) || 16;
    launcherSettings.game_dir = settings?.game_dir || "";
    launcherSettings.default_java = settings?.default_java || "";
    launcherSettings.default_memory_mb = Number(settings?.default_memory_mb) || 2048;
    const savedMirror = settings?.mirror || "auto";
    if (savedMirror && savedMirror.startsWith("http")) {
      customMirror.value = savedMirror;
      launcherSettings.mirror = "custom";
    } else {
      launcherSettings.mirror = savedMirror || "auto";
    }
    devMode.value = Boolean(settings?.dev_mode) || await mcDevModeEnabled();
    setDevLogging(devMode.value);
  } catch (error) {
    ElMessage.error(error.message || "读取设置失败");
  } finally {
    loading.value = false;
  }
}

async function saveLauncherSettings() {
  saving.value = true;
  try {
    let mirror = launcherSettings.mirror;
    if (mirror === "custom") mirror = customMirror.value.trim();
    await mcSaveLauncherSettings({
      concurrency: Math.min(64, Math.max(1, Number(launcherSettings.concurrency) || 16)),
      mirror,
      game_dir: launcherSettings.game_dir.trim(),
      default_java: launcherSettings.default_java.trim(),
      default_memory_mb: Number(launcherSettings.default_memory_mb) || 2048,
    });
    ElMessage.success("设置已保存");
  } catch (error) {
    ElMessage.error(error.message || "保存设置失败");
  } finally {
    saving.value = false;
  }
}

async function toggleDevMode(value) {
  devMode.value = Boolean(value);
  try {
    const enabled = await mcSetDevMode(devMode.value);
    devMode.value = Boolean(enabled);
    setDevLogging(devMode.value);
    ElMessage.success(devMode.value ? "开发者模式已开启：所有操作及反馈将记录到 dev-mode.log" : "开发者模式已关闭");
  } catch (error) {
    devMode.value = !devMode.value;
    setDevLogging(devMode.value);
    ElMessage.error(error.message || "切换开发者模式失败");
  }
}

async function openDevLog() {
  devLogDialogOpen.value = true;
  await refreshDevLog();
  if (devLogListener.value) return;
  devLogListener.value = window.runtime?.EventsOn?.("prism:dev-log", (entry) => {
    if (!devLogDialogOpen.value) return;
    devLogEntries.value = [...devLogEntries.value.filter((item) => item.time !== entry.time), entry].slice(-500);
  });
}

async function refreshDevLog() {
  devLogLoading.value = true;
  try {
    devLogEntries.value = await mcDevLogList() || [];
    devLogPath.value = await mcDevLogPath() || "";
  } catch (error) {
    ElMessage.error(error.message || "读取开发者日志失败");
  } finally {
    devLogLoading.value = false;
  }
}

async function clearDevLog() {
  try {
    await ElMessageBox.confirm("确定清空开发者日志？", "清空日志", { type: "warning" });
  } catch {
    return;
  }
  try {
    await mcDevLogClear();
    await refreshDevLog();
  } catch (error) {
    ElMessage.error(error.message || "清空开发者日志失败");
  }
}

async function openDevLogFile() {
  try {
    await mcOpenDevLog();
  } catch (error) {
    ElMessage.error(error.message || "打开日志文件失败");
  }
}

async function chooseDefaultJava() {
  try {
    const selected = await selectJavaExecutable();
    if (selected) launcherSettings.default_java = selected;
  } catch (error) {
    ElMessage.error(error.message || "选择 Java 路径失败");
  }
}

async function chooseGameDir() {
  try {
    const selected = await selectMCGameDirectory();
    if (selected) launcherSettings.game_dir = selected;
  } catch (error) {
    ElMessage.error(error.message || "选择游戏目录失败");
  }
}

function devLogClass(entry) {
  return entry.ok ? "ok" : "fail";
}

function formatDevTime(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  const pad = (n) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}
</script>

<template>
  <div class="content-stack settings-page" v-loading="loading">
    <div class="page-toolbar">
      <div>
        <h2>总设置</h2>
        <p>开发者模式与全局下载/启动设置</p>
      </div>
    </div>

    <section class="settings-card">
      <div class="settings-card-head">
        <Bug :size="18" />
        <div>
          <h3>开发者模式</h3>
          <p>开启后，所有操作及反馈（界面操作、登录、下载安装、启动游戏、mod 管理等）都会实时记录并输出到日志文件，便于测试与排查。</p>
        </div>
      </div>
      <div class="settings-card-body">
        <div class="settings-row">
          <span>启用开发者模式</span>
          <el-switch :model-value="devMode" @change="toggleDevMode" />
        </div>
        <div class="settings-row muted">
          <span>日志文件：{{ devLogPath || "未生成（开启开发者模式并操作后生成）" }}</span>
        </div>
        <div class="settings-row">
          <el-button plain @click="openDevLog"><Search :size="15" />开发者日志</el-button>
          <el-button plain @click="openDevLogFile"><FolderOpen :size="15" />打开日志文件</el-button>
        </div>
      </div>
    </section>

    <section class="settings-card">
      <div class="settings-card-head">
        <Settings :size="18" />
        <div>
          <h3>全局下载 / 启动设置</h3>
          <p>下载并发与默认镜像、游戏目录、默认 Java 与内存，均为全局默认值。</p>
        </div>
      </div>
      <div class="settings-card-body">
        <div class="settings-grid">
          <div class="field">
            <label>下载并发数（1-64）</label>
            <el-input-number v-model="launcherSettings.concurrency" :min="1" :max="64" />
          </div>
          <div class="field">
            <label>默认分配内存 (MB)</label>
            <el-input-number v-model="launcherSettings.default_memory_mb" :min="512" :step="512" />
          </div>
          <div class="field full">
            <label>默认下载镜像</label>
            <el-select v-model="launcherSettings.mirror" style="width: 100%;">
              <el-option label="自动（测速后选源：官方快则官方优先，否则镜像优先）" value="auto" />
              <el-option label="BMCLAPI 镜像（国内加速，优先镜像）" value="bmclapi" />
              <el-option label="关闭镜像（仅官方源）" value="off" />
              <el-option label="自定义镜像地址" value="custom" />
            </el-select>
            <el-input v-if="launcherSettings.mirror === 'custom'" v-model="customMirror" placeholder="https://你的镜像/minecraft/（路径结构需与官方一致）" style="margin-top: 8px;" />
            <p class="muted-tip">下载失败或过慢时自动切换其他候选源（参照 PCL 的下载方式）。</p>
          </div>
          <div class="field full">
            <label>游戏目录（版本存储根目录）</label>
            <div class="path-input-row">
              <el-input v-model="launcherSettings.game_dir" placeholder="默认 <程序目录>/minecraft，每个版本独立 .minecraft" clearable />
              <el-button @click="chooseGameDir"><FolderOpen :size="16" />浏览</el-button>
            </div>
            <p class="muted-tip">所有已安装版本与下载资源都在该目录下。</p>
          </div>
          <div class="field full">
            <label>默认 Java 路径（可选）</label>
            <div class="path-input-row">
              <el-input v-model="launcherSettings.default_java" placeholder="留空自动选择/自动下载 Java" clearable />
              <el-button @click="chooseDefaultJava"><FolderOpen :size="16" />浏览</el-button>
            </div>
          </div>
        </div>
        <div class="settings-actions">
          <el-button type="primary" :loading="saving" @click="saveLauncherSettings">保存设置</el-button>
        </div>
      </div>
    </section>
  </div>

  <el-dialog v-model="devLogDialogOpen" title="开发者日志" width="760px" @open="openDevLog">
    <div class="mods-toolbar dev-log-toolbar">
      <span class="dev-log-path">日志文件：{{ devLogPath }}</span>
      <div class="mods-toolbar-actions">
        <el-button size="small" plain @click="refreshDevLog"><RefreshCw :size="14" />刷新</el-button>
        <el-button size="small" plain @click="openDevLogFile"><FolderOpen :size="14" />打开文件</el-button>
        <el-button size="small" type="danger" plain @click="clearDevLog"><Trash2 :size="14" />清空</el-button>
      </div>
    </div>
    <div v-loading="devLogLoading" class="dev-log-list">
      <div v-if="!devLogLoading && !devLogEntries.length" class="mods-empty">
        <span>暂无开发者日志。开启开发者模式并操作后，这里会实时显示记录。</span>
      </div>
      <div v-for="entry in devLogEntries.slice().reverse()" :key="entry.time" class="dev-log-row" :class="devLogClass(entry)">
        <span class="dev-log-time">{{ formatDevTime(entry.time) }}</span>
        <span class="dev-log-kind">{{ entry.kind }}</span>
        <span class="dev-log-detail">{{ entry.detail }}</span>
        <span class="dev-log-result" :class="devLogClass(entry)">{{ entry.ok ? "OK" : "FAIL" }}</span>
        <span v-if="!entry.ok" class="dev-log-error">{{ entry.error }}</span>
        <span class="dev-log-elapsed">{{ entry.elapsed }}</span>
      </div>
    </div>
    <template #footer>
      <el-button @click="devLogDialogOpen = false">关闭</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.settings-page { min-width: 0; }
.settings-card { margin-bottom: 14px; padding: 16px 18px; background: var(--app-surface); border: 1px solid var(--app-border); border-radius: 6px; }
.settings-card-head { display: flex; align-items: flex-start; gap: 12px; color: var(--el-color-primary); }
.settings-card-head h3 { margin: 0; font-size: 15px; color: var(--app-text-primary); }
.settings-card-head p { margin: 4px 0 0; color: var(--app-text-secondary); font-size: 12px; }
.settings-card-body { margin-top: 14px; display: grid; gap: 12px; }
.settings-row { display: flex; align-items: center; gap: 12px; color: var(--app-text-primary); font-size: 13px; }
.settings-row.muted { color: var(--app-text-secondary); font-size: 12px; flex-wrap: wrap; }
.settings-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.field { display: grid; gap: 6px; }
.field.full { grid-column: 1 / -1; }
.field label { color: var(--app-text-secondary); font-size: 12px; }
.muted-tip { margin: 4px 0 0; color: var(--app-text-secondary); font-size: 12px; }
.path-input-row { display: flex; width: 100%; gap: 8px; }
.path-input-row .el-input { flex: 1; }
.settings-actions { display: flex; justify-content: flex-end; }
.mods-toolbar { display: flex; gap: 8px; }
.mods-toolbar-actions { display: flex; align-items: center; gap: 6px; }
.dev-log-toolbar { align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; }
.dev-log-path { color: var(--app-text-secondary); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dev-log-list { display: grid; gap: 4px; max-height: 420px; overflow-y: auto; margin-top: 10px; }
.dev-log-row { display: grid; grid-template-columns: 140px 90px 1fr 48px; gap: 8px; align-items: center; padding: 6px 10px; border: 1px solid var(--app-border); border-radius: 6px; font-size: 12px; background: var(--app-surface-muted); }
.dev-log-row.fail { border-color: var(--el-color-danger); }
.dev-log-time { color: var(--app-text-secondary); font-variant-numeric: tabular-nums; }
.dev-log-kind { font-weight: 600; }
.dev-log-detail { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--app-text-primary); }
.dev-log-result.ok { color: var(--el-color-success); font-weight: 600; }
.dev-log-result.fail { color: var(--el-color-danger); font-weight: 600; }
.dev-log-error { grid-column: 1 / -1; color: var(--el-color-danger); overflow-wrap: anywhere; }
.dev-log-elapsed { color: var(--app-text-secondary); font-variant-numeric: tabular-nums; text-align: right; }
.mods-empty { display: grid; place-items: center; gap: 8px; padding: 24px; color: var(--app-text-muted); border: 1px dashed var(--app-border); border-radius: 6px; text-align: center; }
@media (max-width: 900px) {
  .settings-grid { grid-template-columns: 1fr; }
  .dev-log-row { grid-template-columns: 110px 70px 1fr; }
  .dev-log-elapsed { display: none; }
}
</style>
