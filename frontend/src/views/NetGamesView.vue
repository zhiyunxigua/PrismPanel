<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { Activity, FolderOpen, Gamepad2, RefreshCw, Server, Settings, ShieldAlert } from "lucide-vue-next";
import { ElMessage } from "element-plus";
import MetricLineChart from "../components/metrics/MetricLineChart.vue";
import { request } from "../api";
import { sessionState } from "../session";
import { gameJoinProgress, gameServerRunning, gameVersions, isWinApp, joinGameServerConfig, netEaseAccount, selectGameModDirectory } from "../runtime";

const loading = ref(false);
const detailsLoading = ref(false);
const collector = ref(null);
const settings = ref(defaultSettings());
const account = ref(null);
const preference = ref(defaultPreference());
const series = ref(defaultSeries());
const selectedGame = ref(null);
const selectedGameId = ref("");
const gameDrawerOpen = ref(false);
const settingsDialogOpen = ref(false);
const accountForm = ref({ email: "", password: "" });
const preferenceForm = ref({ display_game_count: 10, forced_game_ids_text: "" });
const windowHours = ref(24);
const localNetEaseAccount = ref(null);
const gameVersionOptions = ref([]);
const joinDialogOpen = ref(false);
const joinSubmitting = ref(false);
const joinTarget = ref(null);
const joinProgressDialogOpen = ref(false);
const joinProgress = ref(null);
const joinForm = reactive(defaultJoinForm());
let joinProgressTimer = 0;

const isSuperAdmin = computed(() => sessionState.user?.group?.code === "super_admin");
const games = computed(() => series.value.games || []);
const recentRun = computed(() => collector.value?.last_run || null);
const nextRunAt = computed(() => collector.value?.next_run_at || null);
const chartPalette = ["#c64c4c", "#d88a2d", "#3f8f64", "#397eaf", "#8a63d2", "#c64f8e"];
const joinVersionOptions = computed(() => gameVersionOptions.value.map((item) => ({
  label: `${item.label} · ${String(item.java || "").toUpperCase()}`,
  value: item.version,
})));
const joinProgressPercent = computed(() => Math.round(Number(joinProgress.value?.percent || 0)));
const joinProgressStatusText = computed(() => {
  if (!joinProgress.value) return "尚未开始";
  if (joinProgress.value.running) return "游戏已经在运行中";
  if (joinProgress.value.status === "failed") return joinProgress.value.error || "加入失败";
  return joinProgress.value.message || "处理中";
});

function defaultSettings() {
  return {
    collection_interval_minutes: 15,
    history_retention_days: 30,
    detail_refresh_hours: 24,
    max_detail_batch_size: 24,
  };
}

function defaultJoinForm() {
  return { username: "", version: "", mod_dir: "" };
}

function defaultPreference() {
  return { display_game_count: 10, forced_game_ids: [] };
}

function defaultSeries() {
  return { window_start: null, window_end: null, runs: [], games: [] };
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

function formatList(value) {
  if (Array.isArray(value)) {
    return value.filter(Boolean).join(", ");
  }
  if (typeof value === "string" && value.trim()) {
    return value.trim();
  }
  return "";
}

function gameColor(index) {
  return chartPalette[index % chartPalette.length];
}

function stripHtml(value) {
  if (!value) return "";
  const doc = new DOMParser().parseFromString(String(value), "text/html");
  return doc.body.textContent || "";
}

function applyPreferenceForm(data) {
  preferenceForm.value = {
    display_game_count: data.display_game_count || 10,
    forced_game_ids_text: (data.forced_game_ids || []).join(", "),
  };
}

async function loadAll(silent = false) {
  if (!silent) loading.value = true;
  try {
    const seriesPromise = request(`/api/v1/net-games/series?hours=${windowHours.value}`);
    const preferencePromise = request("/api/v1/user/preferences/net-games").catch(() => ({ preference: defaultPreference() }));
    const collectorPromise = isSuperAdmin.value
      ? request("/api/v1/net-games/collector-status").catch(() => ({ collector: null }))
      : Promise.resolve({ collector: null });
    const settingsPromise = isSuperAdmin.value
      ? request("/api/v1/net-games/settings").catch(() => ({ settings: defaultSettings() }))
      : Promise.resolve({ settings: defaultSettings() });
    const accountPromise = isSuperAdmin.value
      ? request("/api/v1/net-games/account").catch(() => ({ account: null }))
      : Promise.resolve({ account: null });

    const [seriesResult, preferenceResult, collectorResult, settingsResult, accountResult] = await Promise.all([
      seriesPromise,
      preferencePromise,
      collectorPromise,
      settingsPromise,
      accountPromise,
    ]);

    series.value = seriesResult || defaultSeries();
    preference.value = preferenceResult?.preference || defaultPreference();
    applyPreferenceForm(preference.value);
    collector.value = collectorResult?.collector || null;
    settings.value = settingsResult?.settings || defaultSettings();
    account.value = accountResult?.account || null;
    accountForm.value.email = account.value?.email || "";

    if (selectedGameId.value) {
      await loadGame(selectedGameId.value, true);
    }
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) loading.value = false;
  }
}

async function loadGame(gameId, silent = false) {
  if (!gameId) return;
  if (!silent) detailsLoading.value = true;
  selectedGame.value = null;
  try {
    selectedGame.value = await request(`/api/v1/net-games/${encodeURIComponent(gameId)}?hours=${windowHours.value}`);
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) detailsLoading.value = false;
  }
}

function openGame(gameId) {
  selectedGameId.value = gameId;
  gameDrawerOpen.value = true;
  loadGame(gameId);
}

function closeGameDrawer() {
  selectedGameId.value = "";
  selectedGame.value = null;
}

async function savePreference() {
  const forced = preferenceForm.value.forced_game_ids_text
    .split(/[\s,，]+/)
    .map((value) => value.trim())
    .filter(Boolean);
  try {
    const data = await request("/api/v1/user/preferences/net-games", {
      method: "PUT",
      body: JSON.stringify({
        display_game_count: Number(preferenceForm.value.display_game_count) || 10,
        forced_game_ids: forced,
      }),
    });
    preference.value = data.preference;
    settingsDialogOpen.value = false;
    ElMessage.success("个人显示设置已保存");
    await loadAll(true);
  } catch (error) {
    ElMessage.error(error.message);
  }
}

async function saveSettings() {
  try {
    const data = await request("/api/v1/net-games/settings", {
      method: "PUT",
      body: JSON.stringify(settings.value),
    });
    settings.value = data.settings || settings.value;
    ElMessage.success("采集设置已保存");
    await loadAll(true);
  } catch (error) {
    ElMessage.error(error.message);
  }
}

async function verifyAccount() {
  try {
    const data = await request("/api/v1/net-games/account/verify", {
      method: "POST",
      body: JSON.stringify(accountForm.value),
    });
    account.value = data.account;
    accountForm.value.password = "";
    ElMessage.success("网易账号验证成功");
    await loadAll(true);
  } catch (error) {
    ElMessage.error(error.message);
  }
}

async function collectNow() {
  try {
    await request("/api/v1/net-games/collect", { method: "POST", body: "{}" });
    ElMessage.success("已触发立即采集");
    await loadAll(true);
  } catch (error) {
    ElMessage.error(error.message);
  }
}

async function refreshDetails(gameId) {
  try {
    await request(`/api/v1/net-games/${encodeURIComponent(gameId)}/refresh-details`, {
      method: "POST",
      body: "{}",
    });
    ElMessage.success("详情刷新已提交");
    await loadGame(gameId, true);
    await loadAll(true);
  } catch (error) {
    ElMessage.error(error.message);
  }
}

async function deleteAccount() {
  try {
    await request("/api/v1/net-games/account", { method: "DELETE", body: "{}" });
    account.value = null;
    accountForm.value = { email: "", password: "" };
    ElMessage.success("本地账号已删除");
    await loadAll(true);
  } catch (error) {
    ElMessage.error(error.message);
  }
}

async function loadWinAppJoinState() {
  if (!isWinApp()) return;
  try {
    const [accountResult, versionsResult] = await Promise.all([netEaseAccount(), gameVersions()]);
    localNetEaseAccount.value = accountResult || null;
    gameVersionOptions.value = versionsResult || [];
  } catch (error) {
    ElMessage.warning(error.message || "加载本地加入游戏状态失败");
  }
}

async function openNetGameJoin(game) {
  if (!isWinApp()) return;
  await loadWinAppJoinState();
  if (!localNetEaseAccount.value?.email) {
    ElMessage.warning("请先在加入游戏页面登录网易账号");
    return;
  }
  try {
    const detail = await fetchNetGameDetail(game.game_id);
    const endpoint = netGameEndpoint(detail.game);
    if (!endpoint.ip) {
      ElMessage.warning("该网络游戏暂未采集到服务器地址，请先刷新详情");
      return;
    }
    joinTarget.value = { ...detail.game, ip: endpoint.ip, port: endpoint.port };
    Object.assign(joinForm, defaultJoinForm());
    joinForm.version = defaultJoinVersion(detail.game);
    joinDialogOpen.value = true;
  } catch (error) {
    ElMessage.error(error.message || "加载网络游戏详情失败");
  }
}

async function fetchNetGameDetail(gameId) {
  return request(`/api/v1/net-games/${encodeURIComponent(gameId)}?hours=${windowHours.value}`);
}

function netGameEndpoint(game) {
  const directPort = Number(game?.port || 0) || 25565;
  if (game?.ip) return { ip: game.ip, port: directPort };
  const address = String(game?.address || "").trim();
  if (!address) return { ip: "", port: 25565 };
  const match = address.match(/^(.+):(\d+)$/);
  if (!match) return { ip: address, port: 25565 };
  return { ip: match[1], port: Number(match[2]) || 25565 };
}

function defaultJoinVersion(game) {
  const versions = Array.isArray(game?.versions) ? game.versions : [];
  for (const version of versions) {
    const text = String(version);
    const exact = gameVersionOptions.value.find((item) => item.label === text);
    if (exact) return exact.version;
    const compatible = [...gameVersionOptions.value]
      .sort((left, right) => right.label.length - left.label.length)
      .find((item) => text.startsWith(item.label));
    if (compatible) return compatible.version;
  }
  return gameVersionOptions.value[gameVersionOptions.value.length - 1]?.version || "";
}

async function chooseJoinModDirectory() {
  try {
    const selected = await selectGameModDirectory();
    if (selected) joinForm.mod_dir = selected;
  } catch (error) {
    ElMessage.error(error.message || "选择目录失败");
  }
}

async function submitNetGameJoin() {
  if (!joinTarget.value) return;
  if (!joinForm.username.trim()) {
    ElMessage.warning("请输入角色用户名");
    return;
  }
  if (!joinForm.version) {
    ElMessage.warning("请选择游戏版本");
    return;
  }
  if (!joinForm.mod_dir.trim()) {
    ElMessage.warning("请选择自定义资源目录");
    return;
  }
  joinSubmitting.value = true;
  try {
    const input = {
      name: joinTarget.value.name || joinTarget.value.game_id,
      game_id: joinTarget.value.game_id,
      ip: joinTarget.value.ip,
      port: joinTarget.value.port || 25565,
      username: joinForm.username,
      version: joinForm.version,
      mod_dir: joinForm.mod_dir,
    };
    const predicted = await joinGameServerConfig(input);
    joinProgress.value = predicted;
    joinDialogOpen.value = false;
    joinProgressDialogOpen.value = true;
    startJoinProgressPolling(predicted.server_id);
  } catch (error) {
    joinProgress.value = { status: "failed", message: "加入失败", percent: 0, error: error.message || "未知错误" };
    joinProgressDialogOpen.value = true;
    ElMessage.error(error.message || "加入失败");
  } finally {
    joinSubmitting.value = false;
  }
}

function startJoinProgressPolling(serverID) {
  stopJoinProgressPolling();
  if (!serverID) return;
  joinProgressTimer = window.setInterval(async () => {
    try {
      if (await gameServerRunning(serverID)) {
        joinProgress.value = { ...(joinProgress.value || {}), server_id: serverID, status: "done", message: "游戏已经在运行中", percent: 100, running: true };
        stopJoinProgressPolling();
        window.setTimeout(() => { joinProgressDialogOpen.value = false; }, 600);
        return;
      }
      joinProgress.value = await gameJoinProgress(serverID);
      if (joinProgress.value?.status === "done") {
        stopJoinProgressPolling();
        window.setTimeout(() => { joinProgressDialogOpen.value = false; }, 600);
      } else if (joinProgress.value?.status === "failed") {
        stopJoinProgressPolling();
      }
    } catch (error) {
      stopJoinProgressPolling();
      joinProgress.value = { server_id: serverID, status: "failed", message: "读取进度失败", percent: 0, error: error.message || "未知错误" };
    }
  }, 600);
}

function stopJoinProgressPolling() {
  if (joinProgressTimer) window.clearInterval(joinProgressTimer);
  joinProgressTimer = 0;
}

onMounted(() => {
  loadAll();
  loadWinAppJoinState();
});
onBeforeUnmount(stopJoinProgressPolling);

watch(joinProgressDialogOpen, (open) => {
  if (!open) stopJoinProgressPolling();
});
</script>

<template>
  <div class="content-stack net-games-page">
    <div class="page-toolbar">
      <div>
        <h2>网络游戏在线人数</h2>
        <p>{{ series.window_start ? formatTime(series.window_start) : "--" }} - {{ series.window_end ? formatTime(series.window_end) : "--" }}</p>
      </div>
      <div class="toolbar-actions">
        <el-select v-model="windowHours" class="status-filter" @change="loadAll">
          <el-option label="24 小时" :value="24" />
          <el-option label="12 小时" :value="12" />
          <el-option label="6 小时" :value="6" />
          <el-option label="48 小时" :value="48" />
        </el-select>
        <el-tooltip content="刷新">
          <el-button class="square-button" :loading="loading" aria-label="刷新" @click="loadAll">
            <RefreshCw v-if="!loading" :size="16" />
          </el-button>
        </el-tooltip>
        <el-button v-if="isSuperAdmin" @click="collectNow"><Activity :size="16" />立即采集</el-button>
        <el-button v-if="isSuperAdmin" @click="settingsDialogOpen = true"><Settings :size="16" />设置</el-button>
      </div>
    </div>

    <section class="summary-grid">
      <article class="summary-item">
        <div class="summary-icon players"><Server :size="19" /></div>
        <div>
          <span>展示游戏</span>
          <strong>{{ games.length }}</strong>
        </div>
      </article>
      <article class="summary-item">
        <div class="summary-icon nodes"><Activity :size="19" /></div>
        <div>
          <span>采集中</span>
          <strong>{{ collector?.running ? "是" : "否" }}</strong>
        </div>
      </article>
      <article class="summary-item">
        <div class="summary-icon instances"><ShieldAlert :size="19" /></div>
        <div>
          <span>下次采集</span>
          <strong>{{ nextRunAt ? formatTime(nextRunAt) : "--" }}</strong>
        </div>
      </article>
      <article class="summary-item">
        <div class="summary-icon alerts"><Settings :size="19" /></div>
        <div>
          <span>账号状态</span>
          <strong>{{ account?.email || "未配置" }}</strong>
        </div>
      </article>
    </section>

    <section class="net-games-panel">
      <div class="panel-section-head">
        <div>
          <h3>总览</h3>
          <p>按最近成功采集结果排序，点击卡片查看详情</p>
        </div>
      </div>
      <div class="net-games-grid" v-loading="loading">
        <article
          v-for="(game, index) in games"
          :key="game.game_id"
          class="net-game-card"
          :class="{ active: selectedGameId === game.game_id }"
          role="button"
          tabindex="0"
          @click="openGame(game.game_id)"
          @keydown.enter.prevent="openGame(game.game_id)"
          @keydown.space.prevent="openGame(game.game_id)"
        >
          <div class="net-game-card-head">
            <div>
              <strong>{{ game.rank }}. {{ game.name || game.game_id }}</strong>
              <small>{{ game.game_id }}</small>
            </div>
            <div class="net-game-card-actions">
              <span class="net-game-count" :style="{ color: gameColor(index) }">{{ formatCount(game.latest_online_count) }}</span>
              <el-button v-if="isWinApp()" size="small" type="primary" @click.stop="openNetGameJoin(game)">
                <Gamepad2 :size="14" />加入
              </el-button>
            </div>
          </div>
          <MetricLineChart
            :title="game.name || game.game_id"
            :points="(game.points || []).map((point) => ({ sampled_at: point.sampled_at, value: point.value }))"
            :color="gameColor(index)"
            unit=""
            :decimals="0"
          />
        </article>
        <div v-if="!loading && !games.length" class="empty-state">
          <Server :size="26" />
          <strong>暂无可展示的网络游戏数据</strong>
        </div>
      </div>
    </section>

    <section class="net-games-panel">
      <div class="panel-section-head">
        <div>
          <h3>个人显示设置</h3>
          <p>保存到数据库，作用于当前登录用户</p>
        </div>
        <el-button @click="savePreference">保存设置</el-button>
      </div>
      <div class="net-games-settings-grid">
        <el-form-item label="显示数量">
          <el-input-number v-model="preferenceForm.display_game_count" :min="1" :max="20" />
        </el-form-item>
        <el-form-item label="强制显示 ID">
          <el-input v-model="preferenceForm.forced_game_ids_text" placeholder="用逗号分隔多个游戏 ID" />
        </el-form-item>
      </div>
    </section>
  </div>

  <el-drawer v-model="gameDrawerOpen" :with-header="false" size="min(680px, 96vw)" @closed="closeGameDrawer">
    <div v-loading="detailsLoading" class="drawer-detail">
      <div class="drawer-head">
        <div>
          <h3>{{ selectedGame?.game?.name || selectedGameId }}</h3>
          <p>{{ selectedGame?.game?.game_id || selectedGameId }}</p>
        </div>
        <div class="drawer-actions">
          <el-button v-if="isWinApp() && selectedGame?.game" type="primary" @click="openNetGameJoin(selectedGame.game)">
            <Gamepad2 :size="16" />加入
          </el-button>
          <el-button v-if="isSuperAdmin" @click="refreshDetails(selectedGameId)">刷新详情</el-button>
        </div>
      </div>
      <div v-if="selectedGame" class="drawer-grid">
        <div><span>排名</span><strong>{{ selectedGame.rank || "未知" }}</strong></div>
        <div><span>当前在线</span><strong>{{ formatCount(selectedGame.latest_online_count) }}</strong></div>
        <div><span>作者</span><strong>{{ selectedGame.game.author || "未知" }}</strong></div>
        <div><span>版本</span><strong>{{ formatList(selectedGame.game.versions) || "未知" }}</strong></div>
        <div><span>地址</span><strong>{{ selectedGame.game.address || selectedGame.game.ip || "未知" }}</strong></div>
        <div><span>发布时间</span><strong>{{ selectedGame.game.publish_time ? formatTime(selectedGame.game.publish_time * 1000) : "未知" }}</strong></div>
        <div><span>详情状态</span><strong>{{ selectedGame.game.details_status || "未知" }}</strong></div>
        <div><span>图片数量</span><strong>{{ Array.isArray(selectedGame.game.images) ? selectedGame.game.images.length : 0 }}</strong></div>
      </div>
      <div class="drawer-description">
        <h4>简介</h4>
        <p>{{ stripHtml(selectedGame?.game?.description) || "暂无简介" }}</p>
      </div>
      <div v-if="Array.isArray(selectedGame?.game?.images) && selectedGame.game.images.length" class="drawer-description">
        <h4>图片</h4>
        <div class="net-game-image-grid">
          <a
            v-for="(image, index) in selectedGame.game.images"
            :key="image"
            :href="image"
            target="_blank"
            rel="noreferrer"
            class="net-game-image-link"
          >
            <img :src="image" :alt="`${selectedGame.game.name || selectedGame.game.game_id} 图片 ${index + 1}`" loading="lazy" referrerpolicy="no-referrer" />
          </a>
        </div>
      </div>
    </div>
  </el-drawer>

  <el-dialog v-model="joinDialogOpen" :title="joinTarget ? `加入 - ${joinTarget.name || joinTarget.game_id}` : '加入网络游戏'" width="640px">
    <el-form label-position="top" @submit.prevent="submitNetGameJoin">
      <div class="dialog-form-grid">
        <el-form-item label="服务器地址">
          <el-input :model-value="joinTarget ? `${joinTarget.ip}:${joinTarget.port || 25565}` : ''" disabled />
        </el-form-item>
        <el-form-item label="角色用户名">
          <el-input v-model="joinForm.username" placeholder="输入启动游戏使用的角色名" />
        </el-form-item>
        <el-form-item label="游戏版本">
          <el-select v-model="joinForm.version" filterable>
            <el-option v-for="item in joinVersionOptions" :key="item.value" :label="item.label" :value="item.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="自定义资源目录">
          <div class="path-input-row">
            <el-input v-model="joinForm.mod_dir" placeholder="包含 mods/config/resourcepacks/shaderpacks 的目录" />
            <el-button @click="chooseJoinModDirectory"><FolderOpen :size="16" />预览</el-button>
          </div>
        </el-form-item>
      </div>
    </el-form>
    <template #footer>
      <el-button @click="joinDialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="joinSubmitting" @click="submitNetGameJoin">加入</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="joinProgressDialogOpen" :title="joinTarget ? `启动进度 - ${joinTarget.name || joinTarget.game_id}` : '启动进度'" width="520px" :close-on-click-modal="false">
    <div class="launch-progress-body">
      <el-progress :percentage="joinProgressPercent" :status="joinProgress?.status === 'failed' ? 'exception' : joinProgress?.status === 'done' ? 'success' : undefined" />
      <div class="launch-progress-message">
        <strong>{{ joinProgressStatusText }}</strong>
        <span v-if="joinProgress?.stage">阶段：{{ joinProgress.stage }}</span>
      </div>
      <pre v-if="joinProgress?.error" class="launch-error">{{ joinProgress.error }}</pre>
    </div>
    <template #footer>
      <el-button @click="joinProgressDialogOpen = false">关闭</el-button>
    </template>
  </el-dialog>
  <el-dialog v-model="settingsDialogOpen" title="网络游戏设置" width="640px">
    <el-form label-position="top">
      <div v-if="isSuperAdmin" class="dialog-form-grid">
        <el-form-item label="采集间隔（分钟）">
          <el-input-number v-model="settings.collection_interval_minutes" :min="15" :max="60" />
        </el-form-item>
        <el-form-item label="历史保留天数">
          <el-input-number v-model="settings.history_retention_days" :min="1" :max="3650" />
        </el-form-item>
        <el-form-item label="详情刷新间隔（小时）">
          <el-input-number v-model="settings.detail_refresh_hours" :min="1" :max="168" />
        </el-form-item>
        <el-form-item label="单次详情批量">
          <el-input-number v-model="settings.max_detail_batch_size" :min="1" :max="100" />
        </el-form-item>
      </div>
      <div v-if="isSuperAdmin" class="dialog-form-grid">
        <el-form-item label="网易账号邮箱">
          <el-input v-model="accountForm.email" />
        </el-form-item>
        <el-form-item label="网易账号密码">
          <el-input v-model="accountForm.password" type="password" show-password />
        </el-form-item>
      </div>
    </el-form>
    <template #footer>
      <el-button v-if="isSuperAdmin && account?.email" @click="deleteAccount">删除本地账号</el-button>
      <el-button v-if="isSuperAdmin" @click="verifyAccount">验证并保存账号</el-button>
      <el-button v-if="isSuperAdmin" type="primary" @click="saveSettings">保存采集设置</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.net-games-page {
  min-width: 0;
}
.net-games-panel {
  padding: 14px 0 4px;
  border-top: 1px solid #dde4de;
}
.panel-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 14px;
}
.panel-section-head h3 {
  margin: 0;
  font-size: 15px;
}
.panel-section-head p {
  margin: 4px 0 0;
  color: #6d756f;
  font-size: 12px;
}
.net-games-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 12px;
}
.net-game-card {
  padding: 12px;
  border: 1px solid #dce2dd;
  border-radius: 6px;
  background: #fff;
  text-align: left;
  cursor: pointer;
}
.net-game-card.active {
  border-color: #7bb07f;
  box-shadow: 0 0 0 1px rgba(123, 176, 127, 0.22);
}
.net-game-card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}
.net-game-card-head strong {
  display: block;
  font-size: 14px;
}
.net-game-card-head small {
  display: block;
  color: #737d77;
  margin-top: 3px;
}
.net-game-card-actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 8px;
}
.net-game-count {
  font-size: 18px;
  font-weight: 700;
}
.net-games-settings-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 12px;
}
.drawer-detail {
  min-height: 100%;
  display: grid;
  gap: 16px;
}
.drawer-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}
.drawer-head h3 {
  margin: 0;
  font-size: 18px;
}
.drawer-head p {
  margin: 4px 0 0;
  color: #6d756f;
}
.drawer-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}
.drawer-grid div,
.drawer-description {
  padding: 12px;
  border: 1px solid #dce2dd;
  border-radius: 6px;
  background: #fff;
}
.drawer-grid span,
.drawer-description h4 {
  display: block;
  margin: 0 0 6px;
  color: #6d756f;
  font-size: 12px;
}
.drawer-grid strong,
.drawer-description p {
  display: block;
  margin: 0;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
}
.net-game-image-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 10px;
}
.net-game-image-link {
  display: block;
  overflow: hidden;
  border: 1px solid #dce2dd;
  border-radius: 6px;
  background: #f6f8f7;
}
.net-game-image-link img {
  display: block;
  width: 100%;
  height: 140px;
  object-fit: cover;
}
.path-input-row {
  display: flex;
  width: 100%;
  gap: 8px;
}
.path-input-row .el-input {
  flex: 1;
}
.launch-progress-body {
  display: grid;
  gap: 14px;
}
.launch-progress-message strong,
.launch-progress-message span {
  display: block;
}
.launch-progress-message span {
  margin-top: 5px;
  color: #6e7871;
  font-size: 12px;
}
.launch-error {
  overflow: auto;
  margin: 0;
  padding: 10px;
  color: #8b2d24;
  background: #fff4f2;
  border: 1px solid #f2d0ca;
  border-radius: 6px;
  white-space: pre-wrap;
}
@media (max-width: 720px) {
  .net-game-card-head,
  .path-input-row {
    flex-direction: column;
  }
  .net-game-card-actions {
    width: 100%;
    justify-content: space-between;
  }
}
</style>
