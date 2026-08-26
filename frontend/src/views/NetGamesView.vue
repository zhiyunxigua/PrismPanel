<script setup>
import { computed, onMounted, reactive, ref } from "vue";
import { Activity, ArrowLeft, Plus, Search, Settings, Trash2, TrendingUp } from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import MCServerSeriesChart from "../components/MCServerSeriesChart.vue";
import MCServerSparkline from "../components/MCServerSparkline.vue";
import { request } from "../api";
import { sessionState } from "../session";

// 国际版（Minecraft Java 版）服务器在线人数监控
const mcServers = ref([]);
const mcLoading = ref(false);
const mcSeries = ref(defaultMCSeries());
const mcSeriesLoading = ref(false);
const mcTrendKey = ref("");
const mcSummary = ref(null); // GET /mc-servers/summary 聚合结果（摘要卡片 + 表格数据源）
const mcSparkSeries = ref(defaultMCSeries()); // 全服趋势数据，供表格迷你趋势列使用
const overviewStatusFilter = ref("all");
const overviewSearch = ref("");
const overviewSortKey = ref("last_online");
const overviewSortOrder = ref("descending"); // 默认按在线人数降序
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
const mcSparkGames = computed(() => mcSparkSeries.value.games || []);
const mcSparkByKey = computed(() => {
  const map = {};
  for (const game of mcSparkGames.value) map[game.game_id] = game;
  return map;
});
const mcTrendServerName = computed(() => {
  if (!mcTrendKey.value) return "";
  const server = mcServers.value.find((item) => item.server_key === mcTrendKey.value);
  return server?.name || mcTrendKey.value;
});

// ---- 总览摘要卡片 ----
const mcSummaryTotal = computed(() => mcSummary.value?.total_servers ?? mcServers.value.length);
const mcSummaryOnlineServers = computed(() => mcSummary.value?.online_servers ?? mcOnlineCount.value);
const mcSummaryFailed = computed(() => mcSummary.value?.failed_servers ?? mcFailedCount.value);
const mcSummaryTotalOnline = computed(() => mcSummary.value?.total_online ?? 0);
const mcSummaryTotalMax = computed(() => mcSummary.value?.total_max ?? 0);
const mcSummaryAvgLatency = computed(() => {
  if (mcSummary.value?.average_latency_ms != null) return mcSummary.value.average_latency_ms;
  const latencies = mcServers.value
    .filter((server) => server.last_status === "ok" && server.last_latency_ms != null)
    .map((server) => server.last_latency_ms);
  return latencies.length ? Math.round(latencies.reduce((sum, value) => sum + value, 0) / latencies.length) : null;
});

// ---- 总览对比表格：筛选 + 排序 ----
const overviewServers = computed(() => {
  const query = overviewSearch.value.trim().toLowerCase();
  const list = mcServers.value.filter((server) => {
    if (overviewStatusFilter.value !== "all" && server.last_status !== overviewStatusFilter.value) return false;
    if (!query) return true;
    return `${server.name} ${server.host} ${server.port} ${server.server_key}`.toLowerCase().includes(query);
  });
  const ascending = overviewSortOrder.value === "ascending";
  return [...list].sort((left, right) => compareMCServers(left, right, overviewSortKey.value, ascending));
});

function overviewSortValue(server, key) {
  switch (key) {
    case "last_online":
      return server.last_online ?? null;
    case "last_max":
      return server.last_max ?? null;
    case "last_latency_ms":
      return server.last_latency_ms ?? null;
    case "last_checked_at":
      return server.last_checked_at ? new Date(server.last_checked_at).getTime() : null;
    case "last_status":
      return { ok: 0, failed: 1, unknown: 2 }[server.last_status] ?? 3;
    case "name":
      return server.name;
    case "server_key":
      return server.server_key;
    default:
      return null;
  }
}

function compareMCServers(left, right, key, ascending) {
  const a = overviewSortValue(left, key);
  const b = overviewSortValue(right, key);
  if (a === b) return left.id - right.id;
  if (a === null) return 1; // 空值（离线/未采集）始终排最后
  if (b === null) return -1;
  const result = a < b ? -1 : 1;
  return ascending ? result : -result;
}

function onOverviewSortChange({ prop, order }) {
  if (!order || !prop) return;
  overviewSortKey.value = prop;
  overviewSortOrder.value = order;
}

// ---- 迷你趋势（sparkline）----
// 与 MCServerSeriesChart 共用同一调色板，按服务器在 series 中的下标取色，保证图例/折线与表格一致。
const mcSparklinePalette = [
  "#c64c4c", "#397eaf", "#3f8f64", "#d88a2d", "#7a5bb5",
  "#b94d88", "#278b8b", "#8b6537", "#5875c5", "#6f8f3c",
  "#d0603d", "#4f6f62", "#9b4f4f", "#477fa2", "#a06e24",
  "#785f93", "#2f8a69", "#b05b72", "#526f9e", "#708347",
];

function serverSparkline(server) {
  return mcSparkByKey.value[server.server_key]?.points || [];
}

function serverSparklineColor(server) {
  const index = mcSparkGames.value.findIndex((game) => game.game_id === server.server_key);
  if (index < 0) return "#9aa49d";
  return mcSparklinePalette[index % mcSparklinePalette.length];
}

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

// 总览数据：摘要聚合 + 服务器列表，随后并行刷新迷你趋势与图表数据
async function loadMCOverview(silent = false) {
  if (!silent) mcLoading.value = true;
  try {
    const data = await request("/api/v1/net-games/mc-servers/summary");
    mcSummary.value = data;
    mcServers.value = data.servers || [];
    await loadMCSparkSeries(true);
    await loadMCChartSeries(true);
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) mcLoading.value = false;
  }
}

// 全服迷你趋势数据（独立于图表，供对比表格 sparkline 列使用）
async function loadMCSparkSeries(silent = false) {
  const keys = mcServers.value.map((server) => server.server_key);
  if (!keys.length) {
    mcSparkSeries.value = defaultMCSeries();
    return;
  }
  if (!silent) mcSeriesLoading.value = true;
  try {
    mcSparkSeries.value = await request(
      `/api/v1/net-games/mc-servers/series?servers=${encodeURIComponent(keys.join(","))}&hours=${windowHours.value}`,
    );
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) mcSeriesLoading.value = false;
  }
}

// 图表数据：已选中单服时加载该服，否则加载全部服务器做同屏对比
async function loadMCChartSeries(silent = false) {
  const keys = mcTrendKey.value
    ? [mcTrendKey.value]
    : mcServers.value.map((server) => server.server_key);
  if (!keys.length) {
    mcSeries.value = defaultMCSeries();
    return;
  }
  if (!silent) mcSeriesLoading.value = true;
  try {
    mcSeries.value = await request(
      `/api/v1/net-games/mc-servers/series?servers=${encodeURIComponent(keys.join(","))}&hours=${windowHours.value}`,
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
  loadMCChartSeries();
}

// 返回全部对比模式
function showMCCompare() {
  mcTrendKey.value = "";
  loadMCChartSeries();
}

// 点击表格行 → 切换为该服的单服趋势
function onMCRowClick(row, _column, event) {
  if (event?.target?.closest?.("button, a, input, .el-select, .el-switch, .el-checkbox")) return;
  openMCTrend(row);
}

// 点击趋势图上的折线/图例 → 打开对应服务器的单服趋势
function openGameByID(gameID) {
  const server = mcServers.value.find((item) => item.server_key === gameID);
  if (server) openMCTrend(server);
}

function mcRowClassName({ row }) {
  return row.server_key === mcTrendKey.value ? "mc-row-active" : "";
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
    await loadMCOverview(true);
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
    }
    await loadMCOverview(true);
  } catch (error) {
    ElMessage.error(error.message);
  }
}

async function mcCollectNow() {
  mcCollecting.value = true;
  try {
    const data = await request("/api/v1/net-games/mc-servers/collect", { method: "POST", body: "{}" });
    const summary = data.summary || {};
    const checked = summary.checked ?? 0;
    const online = summary.online ?? 0;
    const failed = summary.failed ?? 0;
    if (failed && failed >= checked) {
      ElMessage.error(`采集失败：检查 ${checked} 台全部失败`);
    } else if (failed) {
      ElMessage.warning(`采集完成：检查 ${checked} 台，在线 ${online} 台，失败 ${failed} 台`);
    } else {
      ElMessage.success(`采集完成：检查 ${checked} 台，在线 ${online} 台`);
    }
    await loadMCOverview(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    mcCollecting.value = false;
  }
}

function reloadMCTrend() {
  loadMCChartSeries(true);
  loadMCSparkSeries(true);
}

onMounted(() => {
  loadMCOverview();
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

    <div class="mc-summary-cards">
      <div class="mc-summary-card">
        <span>被监控服务器</span>
        <strong>{{ mcSummaryTotal }}</strong>
      </div>
      <div class="mc-summary-card">
        <span>在线服务器</span>
        <strong class="ok">{{ mcSummaryOnlineServers }}</strong>
        <small>离线 {{ mcSummaryFailed }}</small>
      </div>
      <div class="mc-summary-card">
        <span>当前在线总人数</span>
        <strong>{{ mcSummaryTotalOnline }}</strong>
        <small>总最大 {{ mcSummaryTotalMax }}</small>
      </div>
      <div class="mc-summary-card">
        <span>平均延迟</span>
        <strong>{{ mcSummaryAvgLatency != null ? mcSummaryAvgLatency + " ms" : "--" }}</strong>
      </div>
      <div class="mc-summary-card">
        <span>最后采集</span>
        <strong>{{ mcLastCheckedAt ? formatTime(mcLastCheckedAt) : "--" }}</strong>
      </div>
      <div v-if="isSuperAdmin" class="mc-summary-card">
        <span>采集间隔</span>
        <strong>{{ settings.mc_collection_interval_minutes || 1 }} 分钟</strong>
      </div>
    </div>

    <section class="net-games-panel">
      <div class="mc-overview-toolbar">
        <el-input v-model="overviewSearch" class="mc-search" placeholder="搜索名称 / 地址" clearable>
          <template #prefix><Search :size="14" /></template>
        </el-input>
        <el-select v-model="overviewStatusFilter" class="status-filter" aria-label="按状态筛选">
          <el-option label="全部状态" value="all" />
          <el-option label="在线" value="ok" />
          <el-option label="离线" value="failed" />
          <el-option label="未知" value="unknown" />
        </el-select>
        <span class="mc-sort-hint">默认按在线人数降序，点击表头可排序</span>
      </div>
      <el-table
        :data="overviewServers"
        v-loading="mcLoading"
        size="small"
        class="mc-servers-table"
        :empty-text="mcLoading ? '加载中' : '暂无服务器，点击右上角「添加服务器」'"
        :row-class-name="mcRowClassName"
        @sort-change="onOverviewSortChange"
        @row-click="onMCRowClick"
      >
        <el-table-column prop="name" label="名称" min-width="170" sortable="custom">
          <template #default="{ row }">
            <div class="mc-server-name">
              <strong>{{ row.name }}</strong>
              <small v-if="row.note" :title="row.note">{{ row.note }}</small>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="server_key" label="地址" min-width="170" sortable="custom">
          <template #default="{ row }">{{ row.host }}:{{ row.port }}</template>
        </el-table-column>
        <el-table-column prop="last_status" label="状态" width="86" sortable="custom">
          <template #default="{ row }">
            <el-tag :type="mcStatusType(row)" size="small">{{ mcStatusText(row) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="last_online" label="在线 / 最大" width="112" sortable="custom">
          <template #default="{ row }">
            {{ row.last_status === "ok" ? `${formatCount(row.last_online)} / ${formatCount(row.last_max)}` : "--" }}
          </template>
        </el-table-column>
        <el-table-column prop="last_latency_ms" label="延迟" width="92" sortable="custom">
          <template #default="{ row }">
            {{ row.last_status === "ok" ? `${formatCount(row.last_latency_ms)} ms` : "--" }}
          </template>
        </el-table-column>
        <el-table-column label="版本" min-width="110">
          <template #default="{ row }">{{ row.last_version || "--" }}</template>
        </el-table-column>
        <el-table-column label="迷你趋势" width="150">
          <template #default="{ row }">
            <MCServerSparkline
              v-if="serverSparkline(row).length"
              :points="serverSparkline(row)"
              :color="serverSparklineColor(row)"
              :width="132"
              :height="26"
            />
            <span v-else class="sparkline-empty">--</span>
          </template>
        </el-table-column>
        <el-table-column prop="last_checked_at" label="更新时间" width="118" sortable="custom">
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

    <section class="net-games-panel mc-chart-panel">
      <div class="mc-chart-actions">
        <span class="mc-chart-mode">
          <template v-if="mcTrendKey">单服趋势：{{ mcTrendServerName }}（点击行或图例可切换）</template>
          <template v-else>对比模式：{{ mcServers.length }} 台服务器在线人数同屏比较</template>
        </span>
        <el-button v-if="mcTrendKey" link type="primary" @click="showMCCompare">
          <ArrowLeft :size="14" />返回全部对比
        </el-button>
      </div>
      <MCServerSeriesChart
        :games="mcSeriesGames"
        :window-start="mcSeries.window_start"
        :window-end="mcSeries.window_end"
        :loading="mcSeriesLoading"
        :title="mcTrendKey ? mcTrendServerName + ' 在线人数趋势' : '全部服务器在线人数对比'"
        @open-game="openGameByID"
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
.mc-summary-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(148px, 1fr));
  gap: 10px;
  padding: 4px 0 12px;
}
.mc-summary-card {
  min-width: 0;
  padding: 10px 12px;
  border: 1px solid #dce2dd;
  border-radius: 6px;
  background: #fff;
}
.mc-summary-card span {
  display: block;
  color: #7b857e;
  font-size: 11px;
}
.mc-summary-card strong {
  display: block;
  margin-top: 3px;
  overflow: hidden;
  color: #26322b;
  font-size: 18px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mc-summary-card strong.ok {
  color: #3f8f64;
}
.mc-summary-card small {
  display: block;
  margin-top: 2px;
  color: #8b958e;
  font-size: 10px;
}
.mc-overview-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.mc-search {
  width: 220px;
}
.mc-sort-hint {
  margin-left: auto;
  color: #9aa49d;
  font-size: 11px;
}
.mc-chart-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
}
.mc-chart-mode {
  color: #6d756f;
  font-size: 12px;
}
.sparkline-empty {
  color: #9aa49d;
  font-size: 11px;
}
.mc-row-active td {
  background: #edf6ef !important;
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
