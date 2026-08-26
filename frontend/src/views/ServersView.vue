<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { Activity, ArrowDown, Boxes, CheckSquare, Cpu, ListChecks, MemoryStick, Network, OctagonX, Plus, Power, RefreshCw, Search, Server, Square, SquarePower, Trash2, Users } from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { request } from "../api";
import { importArchive } from "../fileApi";
import { hasPermission, sessionState } from "../session";
import { summarizeBatchResult } from "../batchResult";
import ServerEditorDialog from "../components/servers/ServerEditorDialog.vue";
import OperatorManagementPanel from "../components/operators/OperatorManagementPanel.vue";
import { mergeOnlinePlayers } from "../components/operators/operator-management";

const router = useRouter();
const route = useRoute();
const loading = ref(false);
const submitting = ref(false);
const nodeContents = ref([]);
const dialogOpen = ref(false);
const activeSection = ref("servers");
const search = ref("");
const nodeMemoryKey = "prism:servers:last-node:" + (sessionState.user?.id || "anonymous");
const queryNode = String(route.query.node_id || "");
const nodeFilter = ref(queryNode || rememberedNode());
const stateFilter = ref("");
const typeFilter = ref("");
let refreshTimer;

const canCreate = computed(() => hasPermission("server.create"));
const isSuperAdmin = computed(() => sessionState.user?.group_code === "super_admin");
const nodeOptions = computed(() => nodeContents.value.map((item) => item.node));
const rows = computed(() => nodeContents.value.flatMap((content) => (
  (content.servers || []).map((server) => ({
    ...server,
    node: content.node,
    instances: (content.instances || []).filter((instance) => instance.server_id === server.server_id),
  }))
)));
const filteredRows = computed(() => {
  const keyword = search.value.trim().toLowerCase();
  return rows.value.filter((row) => {
    const state = aggregateState(row.instances);
    const ports = row.type === "standalone" ? [row.port] : row.ports || [];
    return (!nodeFilter.value || row.node.id === nodeFilter.value)
      && (!stateFilter.value || state === stateFilter.value)
      && (!typeFilter.value || serverKind(row) === typeFilter.value)
      && (!keyword || [row.name, row.server_id, ...ports].join(" ").toLowerCase().includes(keyword));
  });
});
const nodeErrors = computed(() => nodeContents.value.filter((item) => item.error));
const runningCount = computed(() => rows.value.reduce(
  (total, row) => total + row.instances.filter((instance) => instance.state === "running").length,
  0,
));
const onlinePlayers = computed(() => mergeOnlinePlayers(nodeContents.value));

// —— 批量操作：勾选服务器组后批量 启停/重启/强停/删除 ——
const selectedKeys = ref(new Set());
const batchSubmitting = ref(false);
const batchActionLabels = { start: "启动", stop: "停止", restart: "重启", kill: "强制停止", delete: "删除" };
const canBatchStart = computed(() => hasPermission("instance.start"));
const canBatchStop = computed(() => hasPermission("instance.stop"));
const canBatchRestart = computed(() => hasPermission("instance.restart"));
const canBatchKill = computed(() => hasPermission("instance.kill"));
const canBatchDelete = computed(() => hasPermission("server.delete"));
const hasBatchActions = computed(() => canBatchStart.value || canBatchStop.value || canBatchRestart.value || canBatchKill.value || canBatchDelete.value);
const selectedRows = computed(() => filteredRows.value.filter((row) => selectedKeys.value.has(selectKey(row))));
const allFilteredSelected = computed(() => (
  filteredRows.value.length > 0 && filteredRows.value.every((row) => selectedKeys.value.has(selectKey(row)))
));

function selectKey(row) {
  return row.node.id + ":" + row.server_id;
}

function isSelected(row) {
  return selectedKeys.value.has(selectKey(row));
}

function toggleSelect(row, value) {
  const next = new Set(selectedKeys.value);
  if (value) next.add(selectKey(row));
  else next.delete(selectKey(row));
  selectedKeys.value = next;
}

function toggleSelectAll() {
  if (allFilteredSelected.value) {
    selectedKeys.value = new Set();
    return;
  }
  selectedKeys.value = new Set(filteredRows.value.map(selectKey));
}

function pruneSelection() {
  const valid = new Set(rows.value.map(selectKey));
  const next = new Set([...selectedKeys.value].filter((key) => valid.has(key)));
  if (next.size !== selectedKeys.value.size) selectedKeys.value = next;
}

async function confirmBatch(action) {
  const count = selectedRows.value.length;
  const names = selectedRows.value.map((row) => row.name).join("、");
  const label = batchActionLabels[action];
  if (action === "kill") {
    const result = await ElMessageBox.prompt(
      "强制停止可能导致未保存的数据丢失。",
      "批量强制停止 " + count + " 个服务器组",
      {
        type: "error",
        inputPlaceholder: "输入「强制停止」确认",
        inputValidator: (value) => value === "强制停止" || "请输入「强制停止」",
        confirmButtonText: "强制停止",
        cancelButtonText: "取消",
      },
    );
    return result.value === "强制停止";
  }
  if (action === "delete") {
    const first = await ElMessageBox.confirm(
      "将删除 " + count + " 个服务器组：" + names + "。\n删除会移除 daemon 上的服务器配置，工作目录和文件不会被删除；仅当组内全部子服已停止时才允许删除。",
      "批量删除服务器组",
      { type: "error", confirmButtonText: "继续", cancelButtonText: "取消" },
    );
    const second = await ElMessageBox.prompt(
      "高风险操作，请输入「删除」完成二次确认。",
      "确认批量删除",
      {
        type: "error",
        inputPlaceholder: "输入「删除」确认",
        inputValidator: (value) => value === "删除" || "请输入「删除」",
        confirmButtonText: "确认删除",
        cancelButtonText: "取消",
      },
    );
    return second.value === "删除";
  }
  const impact = count + " 个服务器组";
  await ElMessageBox.confirm(
    "确认" + label + "选中的" + impact + "（" + names + "）？",
    "批量" + label,
    { type: "warning", confirmButtonText: label, cancelButtonText: "取消" },
  );
  return true;
}

// 批量结果提示：summarizeBatchResult 为共享纯函数（frontend/src/batchResult.js），
// 风格与 t5 的 uploadResult.js 一致（全成功→success、部分失败→warning 带明细、全失败→error）。
async function runBatch(action) {
  if (batchSubmitting.value || !selectedRows.value.length) return;
  const targets = selectedRows.value.map((row) => ({ node_id: row.node.id, server_id: row.server_id }));
  const label = batchActionLabels[action];
  try {
    if (action !== "start") {
      if (!await confirmBatch(action)) return;
    }
  } catch {
    return; // 用户取消
  }
  batchSubmitting.value = true;
  try {
    const data = await request("/api/v1/instances/batch", {
      method: "POST",
      body: JSON.stringify({ action, targets, confirm: action === "delete" }),
    });
    const summary = data?.summary || { total: targets.length, succeeded: 0, failed: targets.length };
    const failures = (data?.results || []).filter((item) => !item.success);
    const result = summarizeBatchResult(summary, failures);
    ElMessage[result.type]("批量" + label + "：" + result.message);
    if (action === "delete") selectedKeys.value = new Set();
    await load(true);
  } catch (error) {
    ElMessage.error("批量" + label + "失败：" + error.message);
  } finally {
    batchSubmitting.value = false;
  }
}

const stateLabels = {
  stopped: "已停止",
  starting: "启动中",
  running: "运行中",
  stopping: "停止中",
  deploying: "部署中",
  failed: "异常",
  mixed: "状态混合",
};

function aggregateState(instances) {
  if (!instances.length) return "stopped";
  if (instances.some((instance) => instance.deployment_locked || instance.state === "deploying")) return "deploying";
  const states = [...new Set(instances.map((instance) => instance.state))];
  if (states.length === 1) return states[0];
  if (states.includes("failed")) return "failed";
  if (states.includes("running")) return "mixed";
  return states[0] || "stopped";
}

function serverKind(row) {
  if (["velocity", "bungee"].includes(row.platform)) return "proxy";
  return row.type === "mirror" ? "mirror" : "ordinary";
}

function serverKindLabel(row) {
  return { ordinary: "普通服务器", mirror: "镜像服", proxy: "代理服" }[serverKind(row)];
}

async function load(silent = false) {
  if (!silent) loading.value = true;
  try {
    const data = await request("/api/v1/servers");
    nodeContents.value = data.nodes || [];
    validateRememberedNode();
    pruneSelection();
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) loading.value = false;
  }
}

function rememberedNode() {
  try {
    return window.localStorage.getItem(nodeMemoryKey) || "";
  } catch {
    return "";
  }
}

function rememberNode(value) {
  try {
    if (value) window.localStorage.setItem(nodeMemoryKey, value);
    else window.localStorage.removeItem(nodeMemoryKey);
  } catch {
    // 浏览器禁用本地存储时仅跳过记忆，不影响筛选。
  }
}

function validateRememberedNode() {
  if (!nodeFilter.value) return;
  const exists = nodeContents.value.some((content) => content.node.id === nodeFilter.value);
  if (exists) {
    rememberNode(nodeFilter.value);
    return;
  }
  nodeFilter.value = "";
  rememberNode("");
  if (route.query.node_id) {
    const query = { ...route.query };
    delete query.node_id;
    router.replace({ query });
  }
}

async function createServer({ nodeId, config, archive, proxyRules, proxyTargets }) {
  submitting.value = true;
  let created = false;
  try {
    const createURL = "/api/v1/servers?node_id=" + encodeURIComponent(nodeId)
      + "&defer_auto_install=true";
    const result = await request(createURL, {
      method: "POST",
      body: JSON.stringify(config),
    });
    created = true;
    let imported = null;
    if (archive) {
      try {
        imported = await importArchive({
          node_id: nodeId,
          resource_type: config.type === "mirror" ? "image" : "instance",
          resource_id: config.server_id,
          path: ".",
          scope: "file.import",
        }, archive);
      } catch (error) {
        throw new Error("压缩包导入失败：" + error.message);
      }
    }
    const installURL = "/api/v1/servers/" + encodeURIComponent(config.server_id)
      + "/plugins/auto-install?node_id=" + encodeURIComponent(nodeId);
    const installResult = await request(installURL, { method: "POST" });
    if (["velocity", "bungee"].includes(config.platform) && Array.isArray(proxyRules)) {
      const query = "?node_id=" + encodeURIComponent(nodeId)
        + "&server_id=" + encodeURIComponent(config.server_id);
      await request("/api/v1/proxy-sync-rules" + query, {
        method: "PUT",
        body: JSON.stringify({ rules: proxyRules || [] }),
      });
    } else if (proxyTargets?.length) {
      const query = "?target_node_id=" + encodeURIComponent(nodeId)
        + "&target_server_id=" + encodeURIComponent(config.server_id);
      await request("/api/v1/proxy-sync-rules" + query, {
        method: "PUT",
        body: JSON.stringify({
          proxies: proxyTargets.map((item) => ({
            node_id: item.node_id,
            server_id: item.server_id,
            enabled: item.enabled,
          })),
        }),
      });
    }
    dialogOpen.value = false;
    if (result?.server?.warnings?.length) ElMessage.warning(result.server.warnings.join("；"));
    const failed = (installResult?.auto_install || []).filter((item) => !item.success);
    if (failed.length) {
      const prefix = imported
        ? "服务器已创建并成功解压，已导入 " + imported.files + " 个文件；"
        : "服务器已创建；";
      const autoStartNotice = installResult?.auto_start_blocked
        ? "，自动启动已禁用"
        : "，且无法自动禁用自动启动，请先检查配置";
      ElMessage.warning(prefix + "但有 " + failed.length + " 个自动安装插件在重试后仍失败" + autoStartNotice);
    } else if (imported) {
      ElMessage.success("服务器已创建并成功解压，已导入 " + imported.files + " 个文件");
    } else {
      ElMessage.success("服务器已创建");
    }
    await load();
  } catch (error) {
    if (created) {
      dialogOpen.value = false;
      await load();
    }
    ElMessage.error(created ? "服务器已创建，但后续处理失败：" + error.message : error.message);
  } finally {
    submitting.value = false;
  }
}

function openDetail(row) {
  router.push({
    name: "server-detail",
    params: { nodeId: row.node.id, serverId: row.server_id },
    query: { node_name: row.node.name },
  });
}

function portSummary(row) {
  const ports = row.type === "standalone" ? [row.port] : row.ports || [];
  if (ports.length <= 3) return ports.join(", ");
  return ports.slice(0, 3).join(", ") + " +" + (ports.length - 3);
}

function sumMetric(instances, field) {
  const values = instances.map((item) => Number(item[field])).filter(Number.isFinite);
  return values.length ? values.reduce((total, value) => total + value, 0) : null;
}

function cpuSummary(row) {
  const value = sumMetric(row.instances, "cpu_percent");
  return value === null ? "--" : value.toFixed(1) + "%";
}

function memorySummary(row) {
  const value = sumMetric(row.instances, "memory_bytes");
  if (value === null) return "--";
  return (value / 1024 / 1024 / 1024).toFixed(2) + " GB";
}

function playerSummary(row) {
  const online = sumMetric(row.instances, "online_players");
  const maximum = sumMetric(row.instances, "max_players");
  if (online === null) return "--";
  return maximum === null ? String(online) : online + " / " + maximum;
}

function tpsSummary(row) {
  const values = row.instances.map((item) => Number(item.tps)).filter(Number.isFinite);
  return values.length ? Math.min(...values).toFixed(1) : "--";
}

onMounted(() => {
  load();
  refreshTimer = window.setInterval(() => load(true), 10000);
});
watch(nodeFilter, (value) => rememberNode(value));
onBeforeUnmount(() => window.clearInterval(refreshTimer));
</script>

<template>
  <div class="content-stack">
    <div class="page-toolbar">
      <div>
        <h2>{{ activeSection === "players" ? "玩家管理" : "服务器" }}</h2>
        <p v-if="activeSection === 'players'">{{ onlinePlayers.length }} 名玩家在线 · 全服统一权限</p>
        <p v-else>{{ rows.length }} 个服务器组 · {{ runningCount }} 个运行实例</p>
      </div>
      <div class="toolbar-actions">
        <template v-if="activeSection === 'servers' && hasBatchActions">
          <el-tooltip :content="allFilteredSelected ? '取消全选' : '全选当前列表'">
            <el-button
              class="square-button"
              :disabled="!filteredRows.length"
              :aria-label="allFilteredSelected ? '取消全选' : '全选'"
              @click="toggleSelectAll"
            >
              <CheckSquare v-if="allFilteredSelected" :size="16" />
              <Square v-else :size="16" />
            </el-button>
          </el-tooltip>
          <el-dropdown
            trigger="click"
            :disabled="!selectedRows.length || batchSubmitting"
            @command="runBatch"
          >
            <el-button :loading="batchSubmitting" :disabled="!selectedRows.length">
              <ListChecks :size="16" />批量操作<template v-if="selectedRows.length">（{{ selectedRows.length }}）</template><el-icon class="el-icon--right"><ArrowDown /></el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item v-if="canBatchStart" command="start" :disabled="batchSubmitting">
                  <Power :size="14" />批量启动
                </el-dropdown-item>
                <el-dropdown-item v-if="canBatchStop" command="stop" :disabled="batchSubmitting">
                  <SquarePower :size="14" />批量停止
                </el-dropdown-item>
                <el-dropdown-item v-if="canBatchRestart" command="restart" :disabled="batchSubmitting">
                  <RefreshCw :size="14" />批量重启
                </el-dropdown-item>
                <el-dropdown-item v-if="canBatchKill" command="kill" :disabled="batchSubmitting" divided>
                  <OctagonX :size="14" />批量强制停止
                </el-dropdown-item>
                <el-dropdown-item
                  v-if="canBatchDelete"
                  command="delete"
                  :disabled="batchSubmitting"
                  divided
                  class="batch-delete-item"
                >
                  <Trash2 :size="14" />批量删除（高风险）
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </template>
        <el-tooltip v-if="activeSection === 'servers'" content="刷新">
          <el-button class="square-button" :loading="loading" aria-label="刷新" @click="load()">
            <RefreshCw v-if="!loading" :size="16" />
          </el-button>
        </el-tooltip>
        <el-button v-if="canCreate && activeSection === 'servers'" type="primary" @click="dialogOpen = true">
          <Plus :size="16" />新增服务器
        </el-button>
      </div>
    </div>

    <el-tabs v-model="activeSection" class="management-tabs">
      <el-tab-pane label="服务器组" name="servers">
        <el-alert
          v-for="item in nodeErrors"
          :key="item.node.id"
          type="warning"
          :closable="false"
          show-icon
          :title="item.node.name + '：' + item.error.message"
        />

        <div class="table-toolbar server-filters">
          <el-input v-model="search" class="search-input" clearable placeholder="搜索名称、ID 或端口">
            <template #prefix><Search :size="15" /></template>
          </el-input>
          <el-select v-model="nodeFilter" class="status-filter" clearable placeholder="全部节点">
            <el-option v-for="node in nodeOptions" :key="node.id" :label="node.name" :value="node.id" />
          </el-select>
          <el-select v-model="stateFilter" class="status-filter" clearable placeholder="全部状态">
            <el-option v-for="(label, value) in stateLabels" :key="value" :label="label" :value="value" />
          </el-select>
          <el-select v-model="typeFilter" class="status-filter" clearable placeholder="全部类型">
            <el-option label="普通服务器" value="ordinary" />
            <el-option label="镜像服" value="mirror" />
            <el-option label="代理服" value="proxy" />
          </el-select>
        </div>

        <div v-loading="loading" class="server-group-grid">
      <div
        v-for="row in filteredRows"
        :key="row.node.id + ':' + row.server_id"
        class="server-group-card-slot"
        :class="{ 'is-selected': isSelected(row) }"
      >
        <el-checkbox
          v-if="hasBatchActions"
          class="server-group-check"
          :model-value="isSelected(row)"
          :aria-label="'选择 ' + row.name"
          @click.stop
          @change="(value) => toggleSelect(row, value)"
        />
        <button
          class="server-group-card"
          type="button"
          @click="openDetail(row)"
        >
        <div class="server-card-head">
          <span class="server-card-symbol">
            <Boxes v-if="row.type === 'mirror'" :size="20" />
            <Network v-else-if="serverKind(row) === 'proxy'" :size="20" />
            <Server v-else :size="20" />
          </span>
          <span class="server-card-state" :class="aggregateState(row.instances)">
            <i />
            {{ stateLabels[aggregateState(row.instances)] || aggregateState(row.instances) }}
          </span>
        </div>
        <div class="server-card-title">
          <strong>{{ row.name }}</strong>
          <div class="server-card-meta">
            <code>{{ row.server_id }}</code>
            <span>{{ serverKindLabel(row) }} · {{ row.platform || "paper" }}</span>
          </div>
        </div>
        <div class="server-card-facts">
          <div><span>节点</span><strong>{{ row.node.name }}</strong></div>
          <div><span>子服</span><strong>{{ row.instances.filter((item) => item.state === "running").length }} / {{ row.instances.length }} 运行</strong></div>
          <div><span>端口</span><strong>{{ portSummary(row) }}</strong></div>
          <div><span>编码</span><strong>{{ row.console?.encoding?.toUpperCase() || "UTF-8" }}</strong></div>
        </div>
        <div class="server-card-metrics">
          <div><Cpu :size="14" /><span>CPU</span><strong>{{ cpuSummary(row) }}</strong></div>
          <div><MemoryStick :size="14" /><span>内存</span><strong>{{ memorySummary(row) }}</strong></div>
          <div><Users :size="14" /><span>玩家</span><strong>{{ playerSummary(row) }}</strong></div>
          <div><Activity :size="14" /><span>TPS</span><strong>{{ tpsSummary(row) }}</strong></div>
        </div>
        </button>
      </div>
      <div v-if="!loading && !filteredRows.length" class="empty-resource server-grid-empty">
        <Server :size="26" />
        <strong>暂无服务器</strong>
      </div>
        </div>
      </el-tab-pane>
      <el-tab-pane v-if="isSuperAdmin" label="玩家管理" name="players" lazy>
        <OperatorManagementPanel :online-players="onlinePlayers" :node-contents="nodeContents" />
      </el-tab-pane>
    </el-tabs>
  </div>

  <ServerEditorDialog
    v-model="dialogOpen"
    :nodes="nodeOptions"
    :node-contents="nodeContents"
    :submitting="submitting"
    @submit="createServer"
  />
</template>
