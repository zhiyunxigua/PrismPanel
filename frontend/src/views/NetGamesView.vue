<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { Activity, Check, Eye, FolderOpen, Gamepad2, ImageOff, RefreshCw, RotateCcw, Save, Search, Settings, Trash2 } from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import NetGameSeriesChart from "../components/NetGameSeriesChart.vue";
import { request } from "../api";
import { sessionState } from "../session";
import { deleteNetGameCharacter, gameJoinProgress, gameServerRunning, isWinApp, joinGameServerConfig, netEaseAccount, netGameLaunchOptions, selectGameModDirectory } from "../runtime";

const loading = ref(false);
const detailsLoading = ref(false);
const collector = ref(null);
const settings = ref(defaultSettings());
const account = ref(null);
const preference = ref(defaultPreference());
const series = ref(defaultSeries());
const catalog = ref(defaultCatalog());
const selectedGameIDs = ref([]);
const savedSelectedGameIDs = ref([]);
const selectionSearch = ref("");
const preferenceSaving = ref(false);
const failedGameImages = reactive(new Set());
const selectedGame = ref(null);
const selectedGameId = ref("");
const gameDrawerOpen = ref(false);
const settingsDialogOpen = ref(false);
const accountForm = ref({ email: "", password: "" });
const windowHours = ref(24);
const localNetEaseAccount = ref(null);
const characters = ref([]);
const characterLoading = ref(false);
const deletingCharacter = ref("");
const joinDialogOpen = ref(false);
const joinSubmitting = ref(false);
const joinTarget = ref(null);
const joinProgressDialogOpen = ref(false);
const joinProgress = ref(null);
const joinForm = reactive(defaultJoinForm());
let joinProgressTimer = 0;

const isSuperAdmin = computed(() => sessionState.user?.group?.code === "super_admin");
const games = computed(() => series.value.games || []);
const catalogGames = computed(() => catalog.value.games || []);
const selectedGameIDSet = computed(() => new Set(selectedGameIDs.value));
const filteredCatalogGames = computed(() => {
  const query = selectionSearch.value.trim().toLocaleLowerCase("zh-CN");
  if (!query) return catalogGames.value;
  return catalogGames.value.filter((game) => [
    game.name,
    game.game_id,
    game.author,
    game.summary,
  ].some((value) => String(value || "").toLocaleLowerCase("zh-CN").includes(query)));
});
const hasSelectionChanges = computed(() => {
  if (selectedGameIDs.value.length !== savedSelectedGameIDs.value.length) return true;
  const saved = new Set(savedSelectedGameIDs.value);
  return selectedGameIDs.value.some((gameID) => !saved.has(gameID));
});
const nextRunAt = computed(() => collector.value?.next_run_at || null);
const joinVersionText = computed(() => joinTarget.value?.version_label || "未识别");
const characterOptions = computed(() => characters.value.map((item) => item.name).filter(Boolean));
const canCreateCharacter = computed(() => characters.value.length < 3);
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
  return { username: "", mod_dir: "" };
}

function defaultPreference() {
  return { selected_game_ids: [] };
}

function defaultSeries() {
  return { window_start: null, window_end: null, runs: [], games: [] };
}

function defaultCatalog() {
  return { sampled_at: null, games: [] };
}

function netGameNicknameKey(gameId) {
  return `prismpanel.net_game.nickname.${gameId}`;
}

function savedNetGameNickname(gameId) {
  try {
    return window.localStorage?.getItem(netGameNicknameKey(gameId)) || "";
  } catch {
    return "";
  }
}

function saveNetGameNickname(gameId, username) {
  try {
    window.localStorage?.setItem(netGameNicknameKey(gameId), username);
  } catch {
    // localStorage 不可用时忽略；启动流程不依赖该缓存。
  }
}

function netGameResourceDirectoryKey(gameId) {
  return `prismpanel.net_game.resource_dir.${gameId}`;
}

function savedNetGameResourceDirectory(gameId) {
  try {
    return window.localStorage?.getItem(netGameResourceDirectoryKey(gameId)) || "";
  } catch {
    return "";
  }
}

function saveNetGameResourceDirectory(gameId, directory) {
  try {
    window.localStorage?.setItem(netGameResourceDirectoryKey(gameId), directory);
  } catch {
    // localStorage 不可用时忽略；启动参数仍会使用本次选择。
  }
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

function stripHtml(value) {
  if (!value) return "";
  const doc = new DOMParser().parseFromString(String(value), "text/html");
  return doc.body.textContent || "";
}

async function loadAll(silent = false) {
  if (!silent) loading.value = true;
  try {
    const seriesPromise = request("/api/v1/net-games/series?hours=" + windowHours.value);
    const catalogPromise = request("/api/v1/net-games/catalog");
    const preferencePromise = request("/api/v1/user/preferences/net-games");
    const collectorPromise = isSuperAdmin.value
      ? request("/api/v1/net-games/collector-status").catch(() => ({ collector: null }))
      : Promise.resolve({ collector: null });
    const settingsPromise = isSuperAdmin.value
      ? request("/api/v1/net-games/settings").catch(() => ({ settings: defaultSettings() }))
      : Promise.resolve({ settings: defaultSettings() });
    const accountPromise = isSuperAdmin.value
      ? request("/api/v1/net-games/account").catch(() => ({ account: null }))
      : Promise.resolve({ account: null });

    const [seriesResult, catalogResult, preferenceResult, collectorResult, settingsResult, accountResult] = await Promise.all([
      seriesPromise,
      catalogPromise,
      preferencePromise,
      collectorPromise,
      settingsPromise,
      accountPromise,
    ]);

    series.value = seriesResult || defaultSeries();
    catalog.value = catalogResult || defaultCatalog();
    preference.value = preferenceResult?.preference || defaultPreference();
    selectedGameIDs.value = [...(preference.value.selected_game_ids || [])];
    savedSelectedGameIDs.value = [...selectedGameIDs.value];
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

async function loadSeries(silent = false) {
  if (!silent) loading.value = true;
  try {
    series.value = await request("/api/v1/net-games/series?hours=" + windowHours.value);
    if (selectedGameId.value) await loadGame(selectedGameId.value, true);
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
  preferenceSaving.value = true;
  try {
    const data = await request("/api/v1/user/preferences/net-games", {
      method: "PUT",
      body: JSON.stringify({ selected_game_ids: selectedGameIDs.value }),
    });
    preference.value = data.preference;
    selectedGameIDs.value = [...(data.preference?.selected_game_ids || [])];
    savedSelectedGameIDs.value = [...selectedGameIDs.value];
    ElMessage.success("显示游戏已保存");
    await loadSeries(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    preferenceSaving.value = false;
  }
}

function toggleGameSelection(gameID) {
  const index = selectedGameIDs.value.indexOf(gameID);
  if (index >= 0) {
    selectedGameIDs.value = selectedGameIDs.value.filter((value) => value !== gameID);
    return;
  }
  if (selectedGameIDs.value.length >= 20) {
    ElMessage.warning("最多选择 20 个游戏");
    return;
  }
  selectedGameIDs.value = [...selectedGameIDs.value, gameID];
}

function selectFilteredGames() {
  const selected = new Set(selectedGameIDs.value);
  const candidates = filteredCatalogGames.value.filter((game) => !selected.has(game.game_id));
  const available = 20 - selectedGameIDs.value.length;
  selectedGameIDs.value = [
    ...selectedGameIDs.value,
    ...candidates.slice(0, Math.max(0, available)).map((game) => game.game_id),
  ];
  if (candidates.length > available) ElMessage.warning("已选择当前结果中的前 20 个游戏");
}

function clearGameSelection() {
  selectedGameIDs.value = [];
}

function restoreGameSelection() {
  selectedGameIDs.value = [...savedSelectedGameIDs.value];
}

function markGameImageFailed(gameID) {
  failedGameImages.add(gameID);
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
    const [accountResult] = await Promise.all([netEaseAccount()]);
    localNetEaseAccount.value = accountResult || null;
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
    characterLoading.value = true;
    const options = await netGameLaunchOptions(game.game_id);
    const endpoint = options.address || {};
    if (!endpoint.ip) {
      ElMessage.warning("该网络游戏暂未返回服务器地址");
      return;
    }
    joinTarget.value = {
      ...game,
      game_id: options.detail.game_id,
      ip: endpoint.ip,
      port: endpoint.port || 25565,
      version_label: options.detail.version_label,
    };
    characters.value = options.characters || [];
    Object.assign(joinForm, defaultJoinForm());
    joinForm.username = savedNetGameNickname(options.detail.game_id) || characterOptions.value[0] || "";
    joinForm.mod_dir = savedNetGameResourceDirectory(options.detail.game_id);
    joinDialogOpen.value = true;
  } catch (error) {
    ElMessage.error(error.message || "加载网络游戏详情失败");
  } finally {
    characterLoading.value = false;
  }
}

async function chooseNetGameResourceDirectory() {
  try {
    const selected = await selectGameModDirectory();
    if (selected) joinForm.mod_dir = selected;
  } catch (error) {
    ElMessage.error(error.message || "选择资源目录失败");
  }
}

async function removeNetGameCharacter(roleName) {
  if (!joinTarget.value?.game_id || !roleName || deletingCharacter.value) return;
  try {
    await ElMessageBox.confirm(`删除网易角色：${roleName}？`, "删除角色", {
      type: "warning", confirmButtonText: "删除", cancelButtonText: "取消",
    });
    deletingCharacter.value = roleName;
    characters.value = await deleteNetGameCharacter(joinTarget.value.game_id, roleName);
    if (joinForm.username === roleName) joinForm.username = characterOptions.value[0] || "";
    ElMessage.success("角色已删除");
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message || "删除角色失败");
  } finally {
    deletingCharacter.value = "";
  }
}

async function submitNetGameJoin() {
  if (!joinTarget.value) return;
  if (!joinForm.username.trim()) {
    ElMessage.warning("请输入角色用户名");
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
      mod_dir: joinForm.mod_dir,
    };
    const predicted = await joinGameServerConfig(input);
    saveNetGameNickname(joinTarget.value.game_id, joinForm.username.trim());
    saveNetGameResourceDirectory(joinTarget.value.game_id, joinForm.mod_dir.trim());
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
        <el-select v-model="windowHours" class="status-filter" @change="loadSeries">
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

    <div class="net-games-status">
      <span>目录 <strong>{{ catalogGames.length }}</strong></span>
      <span>已选 <strong>{{ games.length }}/20</strong></span>
      <span>最后采样 <strong>{{ catalog.sampled_at ? formatTime(catalog.sampled_at) : "--" }}</strong></span>
      <template v-if="isSuperAdmin">
        <span>采集状态 <strong>{{ collector?.running ? "进行中" : "空闲" }}</strong></span>
        <span>下次采集 <strong>{{ nextRunAt ? formatTime(nextRunAt) : "--" }}</strong></span>
        <span>账号 <strong>{{ account?.email || "未配置" }}</strong></span>
      </template>
    </div>

    <section class="net-games-panel">
      <NetGameSeriesChart
        :games="games"
        :window-start="series.window_start"
        :window-end="series.window_end"
        :loading="loading"
        @open-game="openGame"
      />
    </section>

    <section class="net-games-panel game-selection-panel">
      <div class="panel-section-head">
        <div>
          <h3>选择显示游戏</h3>
          <p>选择结果按当前用户保存，最多同时显示 20 个游戏</p>
        </div>
        <div class="selection-save">
          <span>{{ selectedGameIDs.length }}/20</span>
          <el-tooltip content="恢复已保存选择">
            <el-button
              class="square-button"
              :disabled="!hasSelectionChanges"
              aria-label="恢复已保存选择"
              @click="restoreGameSelection"
            >
              <RotateCcw :size="16" />
            </el-button>
          </el-tooltip>
          <el-button
            type="primary"
            :loading="preferenceSaving"
            :disabled="!hasSelectionChanges"
            @click="savePreference"
          >
            <Save :size="16" />保存
          </el-button>
        </div>
      </div>

      <div class="game-selection-toolbar">
        <el-input v-model="selectionSearch" clearable placeholder="搜索游戏名称、作者或 ID">
          <template #prefix><Search :size="15" /></template>
        </el-input>
        <div class="game-selection-actions">
          <el-button @click="selectFilteredGames">全选当前结果</el-button>
          <el-button :disabled="!selectedGameIDs.length" @click="clearGameSelection">清空选择</el-button>
        </div>
      </div>

      <div class="game-selection-result">
        <span>显示 {{ filteredCatalogGames.length }} 个游戏</span>
      </div>

      <div class="net-games-grid" v-loading="loading">
        <article
          v-for="game in filteredCatalogGames"
          :key="game.game_id"
          class="net-game-card"
          :class="{ selected: selectedGameIDSet.has(game.game_id) }"
          role="checkbox"
          :aria-checked="selectedGameIDSet.has(game.game_id)"
          tabindex="0"
          @click="toggleGameSelection(game.game_id)"
          @keydown.enter.prevent="toggleGameSelection(game.game_id)"
          @keydown.space.prevent="toggleGameSelection(game.game_id)"
        >
          <div class="net-game-cover">
            <img
              v-if="game.image && !failedGameImages.has(game.game_id)"
              :src="game.image"
              :alt="game.name || game.game_id"
              loading="lazy"
              referrerpolicy="no-referrer"
              @error="markGameImageFailed(game.game_id)"
            />
            <div v-else class="net-game-cover-placeholder">
              <ImageOff :size="30" />
            </div>
            <div class="net-game-cover-actions">
              <el-button size="small" @click.stop="openGame(game.game_id)">
                <Eye :size="14" />详细信息
              </el-button>
              <el-button v-if="isWinApp()" size="small" type="primary" @click.stop="openNetGameJoin(game)">
                <Gamepad2 :size="14" />加入
              </el-button>
            </div>
          </div>
          <div class="net-game-card-info">
            <div class="net-game-title-row">
              <strong :title="game.name || game.game_id">{{ game.name || game.game_id }}</strong>
              <span>{{ formatCount(game.latest_online_count) }}</span>
            </div>
            <p>{{ game.author || game.summary || "作者信息待采集" }}</p>
            <small>#{{ game.rank }} · {{ game.game_id }}</small>
          </div>
          <div v-if="selectedGameIDSet.has(game.game_id)" class="net-game-check" aria-hidden="true">
            <Check :size="17" />
          </div>
        </article>
        <div v-if="!loading && !filteredCatalogGames.length" class="empty-state">
          <Search :size="25" />
          <strong>{{ catalogGames.length ? "没有匹配的游戏" : "暂无网络游戏数据" }}</strong>
        </div>
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
          <el-select
            v-model="joinForm.username"
            filterable
            :allow-create="canCreateCharacter"
            default-first-option
            :loading="characterLoading"
            :placeholder="canCreateCharacter ? '输入或选择网易已有角色' : '角色已达 3 个，请先删除一个角色'"
          >
            <el-option v-for="character in characters" :key="character.name" :label="character.name" :value="character.name">
              <div class="character-option">
                <span>{{ character.name }}</span>
                <el-button
                  link
                  type="danger"
                  :loading="deletingCharacter === character.name"
                  @mousedown.stop
                  @click.stop.prevent="removeNetGameCharacter(character.name)"
                >
                  <Trash2 v-if="deletingCharacter !== character.name" :size="14" />
                </el-button>
              </div>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item label="游戏版本">
          <el-input :model-value="joinVersionText" disabled />
        </el-form-item>
        <el-form-item label="自定义资源目录" class="full-width-form-item">
          <div class="path-input-row">
            <el-input v-model="joinForm.mod_dir" placeholder="包含 mods/config/resourcepacks/shaderpacks 的目录" />
            <el-button @click="chooseNetGameResourceDirectory"><FolderOpen :size="16" />浏览</el-button>
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
.game-selection-panel {
  padding-top: 18px;
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
.selection-save {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
}
.selection-save > span {
  min-width: 42px;
  color: #5f6b63;
  font-size: 12px;
  text-align: right;
}
.game-selection-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}
.game-selection-toolbar > .el-input {
  width: min(420px, 100%);
}
.game-selection-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.game-selection-result {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 24px;
  margin-bottom: 8px;
  color: #778179;
  font-size: 11px;
}
.net-games-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 12px;
}
.net-game-card {
  position: relative;
  min-width: 0;
  overflow: hidden;
  border: 1px solid #dce2dd;
  border-radius: 6px;
  background: #fff;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.16s ease, box-shadow 0.16s ease, transform 0.16s ease;
}
.net-game-card:hover,
.net-game-card:focus-visible {
  transform: translateY(-2px);
  border-color: #b9c5bd;
  box-shadow: 0 5px 14px rgba(47, 64, 53, 0.09);
  outline: none;
}
.net-game-card.selected {
  border-color: #7bb07f;
  box-shadow: 0 0 0 1px rgba(123, 176, 127, 0.22);
}
.net-game-cover {
  position: relative;
  width: 100%;
  aspect-ratio: 400 / 225;
  overflow: hidden;
  background: #edf1ee;
}
.net-game-cover img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
  transition: transform 0.18s ease;
}
.net-game-card:hover .net-game-cover img {
  transform: scale(1.025);
}
.net-game-cover-placeholder {
  display: grid;
  width: 100%;
  height: 100%;
  place-items: center;
  color: #929d95;
}
.net-game-cover-actions {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-wrap: wrap;
  gap: 7px;
  padding: 10px;
  background: rgba(24, 32, 27, 0.58);
  opacity: 0;
  transition: opacity 0.16s ease;
  pointer-events: none;
}
.net-game-card:hover .net-game-cover-actions,
.net-game-card:focus-within .net-game-cover-actions {
  opacity: 1;
  pointer-events: auto;
}
.net-game-cover-actions .el-button {
  margin: 0;
}
.net-game-card-info {
  min-height: 86px;
  padding: 10px 11px 12px;
}
.net-game-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.net-game-title-row strong {
  min-width: 0;
  overflow: hidden;
  color: #26322b;
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.net-game-title-row span {
  flex: 0 0 auto;
  color: #397eaf;
  font-size: 13px;
  font-weight: 700;
}
.net-game-card-info p {
  min-height: 17px;
  margin: 5px 0 0;
  overflow: hidden;
  color: #6d7870;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.net-game-card-info small {
  display: block;
  margin-top: 5px;
  overflow: hidden;
  color: #929b95;
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.net-game-check {
  position: absolute;
  right: 0;
  bottom: 0;
  display: grid;
  width: 29px;
  height: 29px;
  place-items: center;
  color: #fff;
  background: #4f9960;
  border-radius: 6px 0 0;
}
.net-games-grid > .empty-state {
  grid-column: 1 / -1;
  min-height: 180px;
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
.full-width-form-item {
  grid-column: 1 / -1;
}
.character-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  gap: 8px;
}
.character-option .el-button {
  margin: 0;
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
  .panel-section-head,
  .game-selection-toolbar,
  .path-input-row {
    align-items: stretch;
    flex-direction: column;
  }
  .selection-save,
  .game-selection-actions,
  .game-selection-toolbar > .el-input {
    width: 100%;
  }
  .selection-save > span {
    margin-right: auto;
  }
  .game-selection-actions .el-button {
    flex: 1;
  }
  .net-games-grid {
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 10px;
  }
  .net-game-card-info {
    min-height: 82px;
    padding: 9px;
  }
  .net-game-cover-actions {
    align-content: center;
    opacity: 1;
    background: linear-gradient(to top, rgba(24, 32, 27, 0.68), transparent 70%);
  }
  .net-game-cover-actions .el-button {
    align-self: flex-end;
  }
}
</style>
