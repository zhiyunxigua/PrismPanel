<script setup>
import { computed, onMounted, ref } from "vue";
import { Plus, RefreshCw, ShieldCheck, Trash2, Users } from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { request } from "../../api";
import { normalizeMinecraftUUID, operatorNodeState } from "./operator-management";

const props = defineProps({
  onlinePlayers: { type: Array, default: () => [] },
  nodeContents: { type: Array, default: () => [] },
});

const loading = ref(false);
const submitting = ref(false);
const activating = ref(false);
const response = ref(null);
const dialogOpen = ref(false);
const inputMode = ref("online");
const form = ref({ onlineUUID: "", uuid: "", name: "" });

const state = computed(() => response.value?.state || {
  initialized: false, operators: [], panel_id: "", revision: 0,
});
const operators = computed(() => state.value.operators || []);
const nodes = computed(() => response.value?.nodes || []);
const nodeNames = computed(() => new Map(
  props.nodeContents.map((content) => [content.node.id, content.node.name]),
));

const statusLabels = {
  synced: "已同步",
  pending: "等待同步",
  failed: "同步失败",
  disabled: "功能已关闭",
  uninitialized: "尚未启用",
};
const statusTypes = { synced: "success", pending: "warning", failed: "danger", disabled: "info", uninitialized: "info" };

async function load(silent = false) {
  if (!silent) loading.value = true;
  try {
    response.value = await request("/api/v1/operators");
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) loading.value = false;
  }
}

function openAdd() {
  inputMode.value = props.onlinePlayers.length ? "online" : "uuid";
  form.value = { onlineUUID: "", uuid: "", name: "" };
  dialogOpen.value = true;
}

async function addOperator() {
  let uuid = "";
  let name = "";
  if (inputMode.value === "online") {
    const player = props.onlinePlayers.find((item) => item.uuid === form.value.onlineUUID);
    uuid = player?.uuid || "";
    name = player?.name || "";
  } else {
    uuid = normalizeMinecraftUUID(form.value.uuid);
    name = form.value.name.trim();
  }
  if (!uuid) {
    ElMessage.warning(inputMode.value === "online" ? "请选择在线玩家" : "请输入有效的玩家 UUID");
    return;
  }
  submitting.value = true;
  try {
    response.value = await request("/api/v1/operators/" + encodeURIComponent(uuid), {
      method: "PUT",
      body: JSON.stringify({ name }),
    });
    dialogOpen.value = false;
    ElMessage.success(state.value.initialized ? "全服 OP 已更新" : "玩家已加入待启用名单");
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

async function activate() {
  try {
    await ElMessageBox.confirm(
      `启用后，当前 ${operators.value.length} 名玩家将成为本面板授权的全服 OP；未在所有面板名单中的现有 OP 会被移除。`,
      "启用统一 OP 管理",
      { type: "warning", confirmButtonText: "确认启用", cancelButtonText: "取消" },
    );
  } catch {
    return;
  }
  activating.value = true;
  try {
    response.value = await request("/api/v1/operators/activate", { method: "POST" });
    ElMessage.success("统一 OP 管理已启用");
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    activating.value = false;
  }
}

async function removeOperator(operator) {
  try {
    await ElMessageBox.confirm(
      `移除 ${operator.name || operator.uuid} 的本面板 OP 授权？若其他面板仍授权，该玩家仍会保持 OP。`,
      "移除 OP",
      { type: "warning", confirmButtonText: "确认移除", cancelButtonText: "取消" },
    );
  } catch {
    return;
  }
  try {
    response.value = await request("/api/v1/operators/" + encodeURIComponent(operator.uuid), {
      method: "DELETE",
    });
    ElMessage.success("本面板 OP 授权已移除");
  } catch (error) {
    ElMessage.error(error.message);
  }
}

function formatTime(value) {
  if (!value) return "--";
  return new Date(value).toLocaleString("zh-CN", { hour12: false });
}

function nodeLabel(nodeID) {
  return nodeNames.value.get(nodeID) || nodeID;
}

function targetSummary(node) {
  if (node.error) return node.error;
  const targets = Array.isArray(node.result?.targets) ? node.result.targets : [];
  if (!targets.length) return "当前节点没有可同步的 Spigot/Paper 子服";
  const synced = targets.filter((target) => target.state === "synced").length;
  const failed = targets.filter((target) => target.state === "failed").length;
  return `${synced} / ${targets.length} 个子服已同步${failed ? `，${failed} 个失败` : ""}`;
}

onMounted(load);
</script>

<template>
  <div class="operator-management" v-loading="loading">
    <el-alert
      v-if="response && !response.manage_enabled"
      type="warning"
      :closable="false"
      show-icon
      title="面板配置中的统一 OP 管理当前已关闭"
    />

    <section v-if="response" class="data-section operator-list-section">
      <div class="section-title">
        <div>
          <h3>全服 OP</h3>
          <p>{{ operators.length }} 名玩家 · 本面板来源</p>
        </div>
        <div class="toolbar-actions">
          <el-tooltip content="刷新">
            <el-button class="square-button" aria-label="刷新" :loading="loading" @click="load()">
              <RefreshCw v-if="!loading" :size="16" />
            </el-button>
          </el-tooltip>
          <el-button type="primary" :disabled="!response.manage_enabled" @click="openAdd">
            <Plus :size="16" />添加 OP
          </el-button>
        </div>
      </div>

      <div v-if="!state.initialized" class="operator-activation">
        <div>
          <ShieldCheck :size="20" />
          <span><strong>统一管理尚未启用</strong><small>当前名单仅保存在面板，确认后才会同步到守护进程。</small></span>
        </div>
        <el-button
          type="warning"
          :loading="activating"
          :disabled="!response.manage_enabled"
          @click="activate"
        >
          启用统一管理
        </el-button>
      </div>

      <el-table :data="operators" row-key="uuid">
        <el-table-column label="玩家" min-width="170">
          <template #default="{ row }">
            <strong>{{ row.name || "未知玩家" }}</strong>
            <small class="block muted">{{ row.name ? "本面板授权" : "仅 UUID" }}</small>
          </template>
        </el-table-column>
        <el-table-column label="UUID" min-width="290">
          <template #default="{ row }"><code>{{ row.uuid }}</code></template>
        </el-table-column>
        <el-table-column label="添加人" min-width="130" prop="created_by_username" />
        <el-table-column label="更新时间" min-width="170">
          <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="76" align="right">
          <template #default="{ row }">
            <el-tooltip content="移除 OP">
              <el-button text class="table-action danger" aria-label="移除 OP" @click="removeOperator(row)">
                <Trash2 :size="16" />
              </el-button>
            </el-tooltip>
          </template>
        </el-table-column>
        <template #empty>
          <div class="table-empty"><Users :size="24" /><span>当前面板尚未授权任何 OP</span></div>
        </template>
      </el-table>
    </section>

    <section v-if="response && state.initialized" class="data-section operator-sync-section">
      <div class="section-title">
        <div><h3>节点同步</h3><p>守护进程只返回本面板来源和子服同步状态</p></div>
      </div>
      <el-table :data="nodes" row-key="node_id">
        <el-table-column label="节点" min-width="180">
          <template #default="{ row }"><strong>{{ nodeLabel(row.node_id) }}</strong><small class="block muted">{{ row.node_id }}</small></template>
        </el-table-column>
        <el-table-column label="状态" width="130">
          <template #default="{ row }">
            <el-tag :type="statusTypes[operatorNodeState(row)]" effect="plain">
              {{ statusLabels[operatorNodeState(row)] || operatorNodeState(row) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="子服" min-width="240">
          <template #default="{ row }"><span class="muted">{{ targetSummary(row) }}</span></template>
        </el-table-column>
      </el-table>
    </section>
  </div>

  <el-dialog v-model="dialogOpen" title="添加全服 OP" width="min(520px, 94vw)">
    <el-form label-position="top" @submit.prevent="addOperator">
      <el-form-item label="录入方式">
        <el-segmented
          v-model="inputMode"
          :options="[{ label: '在线玩家', value: 'online' }, { label: '输入 UUID', value: 'uuid' }]"
          block
        />
      </el-form-item>
      <el-form-item v-if="inputMode === 'online'" label="在线玩家">
        <el-select v-model="form.onlineUUID" filterable class="full-control" placeholder="选择玩家">
          <el-option
            v-for="player in onlinePlayers"
            :key="player.uuid"
            :value="player.uuid"
            :label="`${player.name || player.uuid} · ${player.locations.join('、')}`"
          />
        </el-select>
      </el-form-item>
      <template v-else>
        <el-form-item label="玩家 UUID" required>
          <el-input v-model="form.uuid" maxlength="36" placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" />
        </el-form-item>
        <el-form-item label="最近玩家名">
          <el-input v-model="form.name" maxlength="64" placeholder="可选" />
        </el-form-item>
      </template>
    </el-form>
    <template #footer>
      <el-button @click="dialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="addOperator">确认添加</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.operator-management { display: grid; gap: 14px; min-height: 240px; }
.operator-list-section, .operator-sync-section { min-width: 0; }
.operator-activation {
  display: flex;
  min-height: 66px;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 16px;
  color: #835b1f;
  background: #fff8e8;
  border-bottom: 1px solid #ead9ad;
}
.operator-activation > div { display: flex; min-width: 0; align-items: center; gap: 10px; }
.operator-activation span, .operator-activation strong, .operator-activation small { display: block; }
.operator-activation small { margin-top: 3px; color: #8e7857; font-size: 10px; }
:deep(.el-segmented) { width: 100%; }
:global(html.dark) .operator-activation { color: #e2bb75; background: #332b1f; border-color: #59492d; }
:global(html.dark) .operator-activation small { color: #bda77f; }
@media (max-width: 700px) {
  .operator-activation { align-items: stretch; flex-direction: column; }
  .operator-activation .el-button { width: 100%; margin-left: 0; }
}
</style>
