<script setup>
import { computed, onMounted, reactive, ref } from "vue";
import { Activity, Plus, Settings, Trash2, TrendingUp } from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import MCServerSeriesChart from "../components/MCServerSeriesChart.vue";
import { request } from "../api";
import { sessionState } from "../session";

// 国际版（Minecraft Java 版）服务器在线人数监控
const mcServers = ref([]);
const mcLoading = ref(false);
const mcSeries = ref(defaultMCSeries());
const mcSeriesLoading = ref(false);
const mcTrendKey = ref("");
const mcDialogOpen = ref(false);
const mcSaving = ref(false);
const mcCollecting = ref(false);
const mcForm = reactive({ id: 0, name: "", address: "", note: "", enabled: true });
const settings = ref(defaultSettings());
const settingsDialogOpen = ref(false);
const settingsSaving = ref(false);
const windowHours = ref(24);

const isSuperAdmin = computed(() => sessionState.user?.group?.code === "super_admin");
const mcOnlineCount = computed(() => mcServers.value.filter((server) => server.last_status === "ok").length);
const mcFailedCount = computed(() => mcServers.value.filter((server) => server.last_status === "failed").length);
const mcLastCheckedAt = computed(() => {
  const times = mcServers.value.map((server) => server.last_checked_at).filter(Boolean).sort();
  return times.length ? times[times.length - 1] : null;
});
const mcSeriesGames = computed(() => mcSeries.value.games || []);

function defaultSettings() {
  return { history_retention_days: 30, mc_collection_interval_minutes: 1 };
}

function defaultMCSeries() {
  return { window_start: null, window_end: null, games: [] };
}

function formatTime(value) {
  if (!value) return "--";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function formatCount(value) {
  const number = Number(value);
  return Number.isFinite(number) ? String(number) : "--";
}

function mcStatusType(server) {
  if (server.last_status === "ok") return "success";
  if (server.last_status === "failed") return "danger";
  return "info";
}

function mcStatusText(server) {
  if (server.last_status === "ok") return "在线";
  if (server.last_status === "failed") return "离线";
  return "未知";
}

async function loadMCServers(silent = false) {
  if (!silent) mcLoading.value = true;
  try {
    const data = await request("/api/v1/net-games/mc-servers");
    mcServers.value = data.servers || [];
    if (mcTrendKey.value) await loadMCSeries(mcTrendKey.value, true);
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) mcLoading.value = false;
  }
}

async function loadMCSeries(key, silent = false) {
  if (!key) {
    mcSeries.value = defaultMCSeries();
    return;
  }
  if (!silent) mcSeriesLoading.value = true;
  try {
    mcSeries.value = await request(
      `/api/v1/net-games/mc-servers/series?servers=${encodeURIComponent(key)}&hours=${windowHours.value}`,
    );
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) mcSeriesLoading.value = false;
  }
}

async function loadSettings(silent = false) {
  try {
    const data = await request("/api/v1/net-games/settings");
    settings.value = data.settings || defaultSettings();
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  }
}

async function saveSettings() {
  settingsSaving.value = true;
  try {
    const data = await request("/api/v1/net-games/settings", {
      method: "PUT",
      body: JSON.stringify(settings.value),
    });
    settings.value = data.settings || settings.value;
    ElMessage.success("采集设置已保存");
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    settingsSaving.value = false;
  }
}

function openMCAdd() {
  Object.assign(mcForm, { id: 0, name: "", address: "", note: "", enabled: true });
  mcDialogOpen.value = true;
}

function openMCEdit(server) {
  Object.assign(mcForm, {
    id: server.id,
    name: server.name,
    address: `${server.host}:${server.port}`,
    note: server.note || "",
    enabled: !!server.enabled,
  });
  mcDialogOpen.value = true;
}

function openMCTrend(server) {
  mcTrendKey.value = server.server_key;
  loadMCSeries(server.server_key);
}

async function saveMCServer() {
  if (!mcForm.name.trim()) {
    ElMessage.warning("请输入服务器名称");
    return;
  }
  if (!mcForm.address.trim()) {
    ElMessage.warning("请输入服务器地址（IP[:端口]）");
    return;
  }
  mcSaving.value = true;
  try {
    const payload = {
      name: mcForm.name.trim(),
      address: mcForm.address.trim(),
      note: mcForm.note.trim(),
      enabled: mcForm.enabled,
    };
    if (mcForm.id) {
      await request(`/api/v1/net-games/mc-servers/${mcForm.id}`, {
        method: "PUT",
        body: JSON.stringify(payload),
      });
      ElMessage.success("服务器已更新");
    } else {
      await request("/api/v1/net-games/mc-servers", {
        method: "POST",
        body: JSON.stringify(payload),
      });
      ElMessage.success("服务器已添加，正在等待首次采集结果");
    }
    mcDialogOpen.value = false;
    await loadMCServers(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    mcSaving.value = false;
  }
}

async function deleteMCServer(server) {
  try {
    await ElMessageBox.confirm(
      `删除国际版服务器：${server.name}（${server.server_key}）？其历史采集数据将一并删除。`,
      "删除服务器",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" },
    );
  } catch {
    return;
  }
  try {
    await request(`/api/v1/net-games/mc-servers/${server.id}`, { method: "DELETE", body: "{}" });
    ElMessage.success("服务器已删除");
    if (mcTrendKey.value === server.server_key) {
      mcTrendKey.value = "";
      mcSeries.value = defaultMCSeries();
    }
    await loadMCServers(true);
  } catch (error) {
    ElMessage.error(error.message);
  }
}

async function mcCollectNow() {
  mcCollecting.value = true;
  try {
    const data = await request("/api/v1/net-games/mc-servers/collect", { method: "POST", body: "{}" });
    const summary = data.summary || {};
    ElMessage.success(`采集完成：检查 ${summary.checked ?? 0} 台，在线 ${summary.online ?? 0} 台，失败 ${summary.failed ?? 0} 台`);
    await loadMCServers(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    mcCollecting.value = false;
  }
}

function reloadMCTrend() {
  if (mcTrendKey.value) loadMCSeries(mcTrendKey.value, true);
}

onMounted(() => {
  loadMCServers();
  if (isSuperAdmin.value) loadSettings();
});
</script>

<template>
  <div class="content-stack net-games-page">
    <div class="page-toolbar">
      <div>
        <h2>服务器监控</h2>
        <p>手动添加 Minecraft Java 版服务器（IP[:端口]），面板服务端按设置间隔自动采集在线人数并绘制趋势</p>
      </div>
      <div class="toolbar-actions">
        <el-select v-model="windowHours" class="status-filter" @change="reloadMCTrend">
          <el-option label="24 小时" :value="24" />
          <el-option label="12 小时" :value="12" />
          <el-option label="6 小时" :value="6" />
          <el-option label="48 小时" :value="48" />
        </el-select>
        <el-button v-if="isSuperAdmin" @click="settingsDialogOpen = true"><Settings :size="16" />设置</el-button>
        <el-button v-if="isSuperAdmin" :loading="mcCollecting" @click="mcCollectNow"><Activity :size="16" />立即采集</el-button>
        <el-button v-if="isSuperAdmin" type="primary" @click="openMCAdd"><Plus :size="16" />添加服务器</el-button>
      </div>
    </div>

    <div class="net-games-status">
      <span>服务器 <strong>{{ mcServers.length }}</strong></span>
      <span>在线 <strong>{{ mcOnlineCount }}</strong></span>
      <span>离线 <strong>{{ mcFailedCount }}</strong></span>
      <span>最后采集 <strong>{{ mcLastCheckedAt ? formatTime(mcLastCheckedAt) : "--" }}</strong></span>
      <span v-if="isSuperAdmin">采集间隔 <strong>{{ settings.mc_collection_interval_minutes || 1 }} 分钟</strong></span>
    </div>

    <section class="net-games-panel">
      <el-table
        :data="mcServers"
        v-loading="mcLoading"
        size="small"
        class="mc-servers-table"
        :empty-text="mcLoading ? '加载中' : '暂无服务器，点击右上角「添加服务器」'"
      >
        <el-table-column label="名称" min-width="170">
          <template #default="{ row }">
            <div class="mc-server-name">
              <strong>{{ row.name }}</strong>
              <small v-if="row.note" :title="row.note">{{ row.note }}</small>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="地址" min-width="170">
          <template #default="{ row }">{{ row.host }}:{{ row.port }}</template>
        </el-table-column>
        <el-table-column label="状态" width="78">
          <template #default="{ row }">
            <el-tag :type="mcStatusType(row)" size="small">{{ mcStatusText(row) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="在线 / 最大" width="104">
          <template #default="{ row }">
            {{ row.last_status === "ok" ? `${formatCount(row.last_online)} / ${formatCount(row.last_max)}` : "--" }}
          </template>
        </el-table-column>
        <el-table-column label="延迟" width="92">
          <template #default="{ row }">
            {{ row.last_status === "ok" ? `${formatCount(row.last_latency_ms)} ms` : "--" }}
          </template>
        </el-table-column>
        <el-table-column label="版本" min-width="110">
          <template #default="{ row }">{{ row.last_version || "--" }}</template>
        </el-table-column>
        <el-table-column label="更新时间" width="118">
          <template #default="{ row }">{{ row.last_checked_at ? formatTime(row.last_checked_at) : "--" }}</template>
        </el-table-column>
        <el-table-column label="操作" width="168" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openMCTrend(row)"><TrendingUp :size="14" />趋势</el-button>
            <el-button v-if="isSuperAdmin" link @click="openMCEdit(row)">编辑</el-button>
            <el-button v-if="isSuperAdmin" link type="danger" @click="deleteMCServer(row)"><Trash2 :size="14" />删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </section>

    <section v-if="mcTrendKey" class="net-games-panel">
      <MCServerSeriesChart
        :games="mcSeriesGames"
        :window-start="mcSeries.window_start"
        :window-end="mcSeries.window_end"
        :loading="mcSeriesLoading"
        @open-game="() => {}"
      />
    </section>
  </div>

  <el-dialog v-model="mcDialogOpen" :title="mcForm.id ? '编辑国际版服务器' : '添加国际版服务器'" width="540px">
    <el-form label-position="top" @submit.prevent="saveMCServer">
      <el-form-item label="名称">
        <el-input v-model="mcForm.name" placeholder="如：我的世界原版服务器" maxlength="100" />
      </el-form-item>
      <el-form-item label="服务器地址（IP[:端口]，默认端口 25565）">
        <el-input v-model="mcForm.address" placeholder="如：play.example.com 或 play.example.com:25565" />
      </el-form-item>
      <el-form-item label="备注">
        <el-input v-model="mcForm.note" maxlength="500" placeholder="选填" />
      </el-form-item>
      <el-form-item label="启用自动采集">
        <el-switch v-model="mcForm.enabled" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="mcDialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="mcSaving" @click="saveMCServer">保存</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="settingsDialogOpen" title="采集设置" width="480px">
    <el-form label-position="top">
      <div class="dialog-form-grid">
        <el-form-item label="国际版采集间隔（分钟）">
          <el-input-number v-model="settings.mc_collection_interval_minutes" :min="1" :max="60" />
        </el-form-item>
        <el-form-item label="历史保留天数">
          <el-input-number v-model="settings.history_retention_days" :min="1" :max="3650" />
        </el-form-item>
      </div>
      <p class="settings-hint">国际版采集间隔修改后立即热生效，无需重启面板。</p>
    </el-form>
    <template #footer>
      <el-button @click="settingsDialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="settingsSaving" @click="saveSettings">保存设置</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.net-games-page {
  min-width: 0;
}
.net-games-status {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 7px 18px;
  min-height: 32px;
  padding: 4px 0 10px;
  color: #737d76;
  font-size: 11px;
}
.net-games-status strong {
  margin-left: 4px;
  color: #334039;
  font-weight: 600;
}
.net-games-panel {
  padding: 14px 0 4px;
  border-top: 1px solid #dde4de;
}
.mc-servers-table {
  width: 100%;
}
.mc-server-name {
  min-width: 0;
}
.mc-server-name strong {
  display: block;
  overflow: hidden;
  color: #26322b;
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mc-server-name small {
  display: block;
  margin-top: 2px;
  overflow: hidden;
  color: #8b958e;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.settings-hint {
  margin: 4px 0 0;
  color: #778179;
  font-size: 11px;
}
</style>
