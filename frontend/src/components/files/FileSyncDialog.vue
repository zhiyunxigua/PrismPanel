<script setup>
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { ArrowRightLeft, CircleAlert, RefreshCw } from "lucide-vue-next";
import { ElMessage } from "element-plus";
import { request } from "../../api";
import InstanceTargetSelectionTree from "../InstanceTargetSelectionTree.vue";

const props = defineProps({
  visible: { type: Boolean, default: false },
  source: { type: Object, default: () => ({}) },
  entries: { type: Array, default: () => [] },
  nodes: { type: Array, default: () => [] },
});
const emit = defineEmits(["update:visible"]);
const targets = ref([]);
const task = ref(null);
const submitting = ref(false);
let timer;

const sourceLabel = computed(() => `${props.source.node_name || props.source.node_id || "源节点"} / ${props.source.resource_id || "源资源"}`);
const active = computed(() => task.value && ["queued", "running", "cancel_requested"].includes(task.value.status));
const failed = computed(() => (task.value?.items || []).filter((item) => item.status === "failed"));
const progress = computed(() => {
  const total = Number(task.value?.total) || 0;
  return total ? Math.round((Number(task.value.completed) || 0) / total * 100) : 0;
});

watch(() => props.visible, (visible) => {
  if (!visible) return;
  targets.value = [];
  task.value = null;
});

function formatError(item) {
  if (!item) return "";
  return [item.error_message, ...(item.error_details || [])].filter(Boolean).join("；");
}
function taskPath(entry) { return entry.path || entry.id; }
function statusLabel(status) {
  return {
    queued: "等待同步", running: "同步中", completed: "已完成",
    completed_with_errors: "完成，部分失败", failed: "失败", cancelled: "已取消",
  }[status] || status;
}
function stageLabel(stage) {
  return { listing: "读取源目录", downloading: "读取源文件", uploading: "写入目标", creating_directory: "创建目录", completed: "完成", failed: "失败" }[stage] || stage;
}
function startPolling() {
  window.clearInterval(timer);
  timer = window.setInterval(refreshTask, 1000);
}
async function refreshTask() {
  if (!task.value?.task_id) return;
  try {
    task.value = await request(`/api/v1/file-syncs/${encodeURIComponent(task.value.task_id)}`);
    if (!active.value) window.clearInterval(timer);
  } catch (error) {
    window.clearInterval(timer);
    ElMessage.error(error.message || "同步任务读取失败");
  }
}
async function start() {
  if (!targets.value.length || !props.entries.length) return;
  submitting.value = true;
  try {
    task.value = await request("/api/v1/file-syncs", {
      method: "POST",
      body: JSON.stringify({
        source: {
          node_id: props.source.node_id,
          resource_type: props.source.resource_type,
          resource_id: props.source.resource_id,
        },
        paths: props.entries.map((entry) => ({ path: entry.path, type: entry.type })),
        targets: targets.value,
      }),
    });
    startPolling();
  } catch (error) {
    ElMessage.error(error.message || "同步任务创建失败");
  } finally {
    submitting.value = false;
  }
}
async function retry() {
  if (!task.value?.task_id) return;
  try {
    task.value = await request(`/api/v1/file-syncs/${encodeURIComponent(task.value.task_id)}/retry`, { method: "POST", body: "{}" });
    startPolling();
  } catch (error) {
    ElMessage.error(error.message || "重试失败");
  }
}
async function cancel() {
  if (!task.value?.task_id) return;
  try {
    task.value = await request(`/api/v1/file-syncs/${encodeURIComponent(task.value.task_id)}/cancel`, { method: "POST", body: "{}" });
  } catch (error) {
    ElMessage.error(error.message || "取消失败");
  }
}
onBeforeUnmount(() => window.clearInterval(timer));
</script>

<template>
  <el-dialog
    :model-value="visible"
    title="同步文件到子服"
    width="min(860px, 96vw)"
    :close-on-click-modal="false"
    @update:model-value="emit('update:visible', $event)"
  >
    <template v-if="!task">
      <div class="sync-source"><span>同步源</span><strong>{{ sourceLabel }}</strong></div>
      <div class="sync-source"><span>已选文件</span><strong>{{ entries.length }} 项</strong></div>
      <div class="sync-paths">
        <code v-for="entry in entries" :key="entry.path">{{ entry.path }}</code>
      </div>
      <div class="sync-section-title"><div><strong>目标子服</strong><p>文件会直接覆盖目标同路径文件，不会停止或重启服务器。</p></div><span>{{ targets.length }} 个已选</span></div>
      <InstanceTargetSelectionTree v-model="targets" :nodes="nodes" :source="source" :disabled="submitting" />
    </template>
    <template v-else>
      <div class="sync-summary">
        <div><span>任务状态</span><strong>{{ statusLabel(task.status) }}</strong></div>
        <div><span>完成</span><strong>{{ task.completed || 0 }} / {{ task.total || 0 }}</strong></div>
        <div><span>失败</span><strong :class="{ danger: task.failed }">{{ task.failed || 0 }}</strong></div>
        <el-progress :percentage="progress" :status="task.status === 'completed' ? 'success' : (task.failed ? 'exception' : undefined)" />
      </div>
      <div class="sync-items">
        <div v-for="item in task.items || []" :key="item.id" class="sync-item" :class="`is-${item.status}`">
          <div class="sync-item-main"><strong>{{ item.instance_id }}</strong><code>{{ taskPath(item) }}</code></div>
          <div class="sync-item-status">{{ stageLabel(item.stage) || statusLabel(item.status) }}</div>
          <div v-if="item.status === 'failed'" class="sync-item-error">
            <CircleAlert :size="14" /><span>{{ item.error_code }}：{{ formatError(item) }}</span>
            <small v-if="item.retry_after_stop">请关闭目标服务器后重试</small>
          </div>
        </div>
        <div v-if="!task.items?.length" class="sync-empty">正在读取源目录...</div>
      </div>
    </template>
    <template #footer>
      <el-button v-if="active" @click="cancel">取消任务</el-button>
      <el-button v-if="failed.length && !active" @click="retry"><RefreshCw :size="14" />重试失败项</el-button>
      <el-button v-if="!task" type="primary" :disabled="!targets.length || !entries.length" :loading="submitting" @click="start"><ArrowRightLeft :size="15" />开始同步</el-button>
      <el-button v-else type="primary" @click="emit('update:visible', false)">关闭</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.sync-source { display: flex; justify-content: space-between; gap: 18px; padding: 8px 0; color: var(--app-text-muted); font-size: 12px; }
.sync-source strong { color: var(--app-text); font-weight: 600; }
.sync-paths { display: grid; max-height: 110px; overflow: auto; gap: 4px; border: 1px solid var(--app-border); margin: 8px 0 18px; padding: 8px; background: var(--app-surface-muted); }
.sync-paths code { overflow: hidden; color: var(--app-text-secondary); font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.sync-section-title { display: flex; align-items: flex-end; justify-content: space-between; gap: 12px; margin-bottom: 8px; }
.sync-section-title p { margin: 5px 0 0; color: var(--app-text-muted); font-size: 12px; }
.sync-section-title > span { color: var(--app-accent); font-size: 12px; }
.sync-summary { display: grid; grid-template-columns: repeat(3, minmax(90px, 1fr)); gap: 12px; margin-bottom: 14px; }
.sync-summary > div { display: grid; gap: 4px; border: 1px solid var(--app-border); padding: 8px 10px; }
.sync-summary span { color: var(--app-text-muted); font-size: 11px; }
.sync-summary strong { color: var(--app-text); }
.sync-summary :deep(.el-progress) { grid-column: 1 / -1; }
.sync-items { max-height: 430px; overflow: auto; border: 1px solid var(--app-border); }
.sync-item { display: grid; grid-template-columns: minmax(0, 1fr) 90px; gap: 8px 14px; border-bottom: 1px solid var(--app-border-soft); padding: 9px 11px; }
.sync-item:last-child { border-bottom: 0; }
.sync-item-main { display: grid; min-width: 0; gap: 3px; }
.sync-item-main strong, .sync-item-main code { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sync-item-main strong { color: var(--app-text); font-size: 12px; }
.sync-item-main code { color: var(--app-text-muted); font-size: 11px; }
.sync-item-status { align-self: center; color: var(--app-text-muted); font-size: 11px; text-align: right; }
.sync-item.is-completed .sync-item-status { color: #37834b; }
.sync-item.is-failed .sync-item-status, .danger { color: #b84a41; }
.sync-item-error { display: flex; grid-column: 1 / -1; align-items: center; gap: 5px; color: #b84a41; font-size: 11px; }
.sync-item-error small { margin-left: auto; color: #9b6a27; }
.sync-empty { padding: 28px; color: var(--app-text-muted); text-align: center; font-size: 12px; }
@media (max-width: 640px) { .sync-summary { grid-template-columns: 1fr 1fr; } .sync-summary :deep(.el-progress) { grid-column: 1 / -1; } .sync-item { grid-template-columns: 1fr; } .sync-item-status { text-align: left; } .sync-item-error small { margin-left: 0; } }
</style>
