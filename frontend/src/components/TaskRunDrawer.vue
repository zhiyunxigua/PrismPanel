<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import {
  ChevronDown, ChevronRight, CircleCheck, CircleX, Clock3, RefreshCw, TerminalSquare,
} from "lucide-vue-next";
import { ElMessage } from "element-plus";
import { request } from "../api";

const props = defineProps({
  modelValue: { type: Boolean, default: false },
});
const emit = defineEmits(["update:modelValue", "running-count"]);

const loading = ref(false);
const loadingMore = ref(false);
const items = ref([]);
const definitions = ref([]);
const nextCursor = ref("");
const hasMore = ref(false);
const runningCount = ref(0);
const taskFilter = ref("");
const statusFilter = ref("");
const dateRange = ref([]);
const expandedID = ref("");
const detailLoading = ref("");
const details = ref({});
const scrollContainer = ref();
let refreshTimer;

const statusLabels = {
  queued: "等待执行",
  running: "执行中",
  completed: "成功",
  completed_with_errors: "部分失败",
  failed: "失败",
  missed: "已错过",
};
const statusTypes = {
  queued: "info",
  running: "warning",
  completed: "success",
  completed_with_errors: "warning",
  failed: "danger",
  missed: "info",
};
const actionLabels = {
  start: "启动实例",
  stop: "停止实例",
  restart: "重启实例",
  kill: "强制停止",
  command: "发送控制台命令",
};

function buildQuery(cursor = "") {
  const query = new URLSearchParams({ limit: "30" });
  if (taskFilter.value) query.set("scheduled_task_id", taskFilter.value);
  if (statusFilter.value) query.set("status", statusFilter.value);
  if (dateRange.value?.length === 2) {
    query.set("from", new Date(dateRange.value[0]).toISOString());
    query.set("to", new Date(dateRange.value[1]).toISOString());
  }
  if (cursor) query.set("cursor", cursor);
  return query.toString();
}

async function loadFirst(silent = false) {
  if (!silent) loading.value = true;
  try {
    const data = await request("/api/v1/task-runs?" + buildQuery());
    items.value = data.items || [];
    nextCursor.value = data.next_cursor || "";
    hasMore.value = Boolean(data.has_more);
    setRunningCount(data.running_count || 0);
    expandedID.value = "";
    details.value = {};
    await nextTick();
    if (scrollContainer.value) scrollContainer.value.scrollTop = 0;
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) loading.value = false;
  }
}

async function refreshHead() {
  try {
    const data = await request("/api/v1/task-runs?" + buildQuery());
    const latest = data.items || [];
    const latestIDs = new Set(latest.map((item) => item.id));
    items.value = [...latest, ...items.value.filter((item) => !latestIDs.has(item.id))];
    if (items.value.length <= 30) {
      nextCursor.value = data.next_cursor || "";
      hasMore.value = Boolean(data.has_more);
    }
    setRunningCount(data.running_count || 0);
  } catch {
    // 后台刷新失败时保留当前日志，下一轮继续尝试。
  }
}

async function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value || !nextCursor.value) return;
  loadingMore.value = true;
  try {
    const data = await request("/api/v1/task-runs?" + buildQuery(nextCursor.value));
    const existing = new Set(items.value.map((item) => item.id));
    items.value.push(...(data.items || []).filter((item) => !existing.has(item.id)));
    nextCursor.value = data.next_cursor || "";
    hasMore.value = Boolean(data.has_more);
    setRunningCount(data.running_count || 0);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    loadingMore.value = false;
  }
}

function handleScroll(event) {
  const target = event.currentTarget;
  if (target.scrollHeight - target.scrollTop - target.clientHeight < 120) loadMore();
}

async function toggleDetail(run) {
  if (expandedID.value === run.id) {
    expandedID.value = "";
    return;
  }
  expandedID.value = run.id;
  if (details.value[run.id]) return;
  detailLoading.value = run.id;
  try {
    details.value = {
      ...details.value,
      [run.id]: await request("/api/v1/task-runs/" + encodeURIComponent(run.id)),
    };
  } catch (error) {
    ElMessage.error(error.message);
    expandedID.value = "";
  } finally {
    detailLoading.value = "";
  }
}

async function loadDefinitions() {
  try {
    const data = await request("/api/v1/scheduled-tasks");
    definitions.value = data.items || [];
  } catch {
    definitions.value = [];
  }
}

async function loadCount() {
  try {
    const data = await request("/api/v1/task-runs?limit=1");
    setRunningCount(data.running_count || 0);
  } catch {
    setRunningCount(0);
  }
}

function setRunningCount(value) {
  runningCount.value = Number(value) || 0;
  emit("running-count", runningCount.value);
}

function formatDate(value) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    month: "2-digit", day: "2-digit", hour: "2-digit",
    minute: "2-digit", second: "2-digit",
  }).format(new Date(value));
}

function duration(run) {
  if (!run.started_at || !run.finished_at) return "";
  const milliseconds = new Date(run.finished_at) - new Date(run.started_at);
  if (milliseconds < 1000) return milliseconds + " ms";
  return (milliseconds / 1000).toFixed(1) + " s";
}

watch(() => props.modelValue, (open) => {
  if (!open) return;
  loadDefinitions();
  loadFirst();
});

watch([taskFilter, statusFilter, dateRange], () => {
  if (props.modelValue) loadFirst();
}, { deep: true });

onMounted(() => {
  loadCount();
  refreshTimer = window.setInterval(() => {
    if (props.modelValue) refreshHead();
    else loadCount();
  }, 8000);
});
onBeforeUnmount(() => window.clearInterval(refreshTimer));
</script>

<template>
  <el-drawer
    :model-value="modelValue"
    title="任务执行日志"
    size="min(620px, 96vw)"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="task-log-drawer">
      <div class="task-log-filters">
        <el-select v-model="taskFilter" clearable placeholder="全部定时任务">
          <el-option v-for="task in definitions" :key="task.id" :label="task.name" :value="task.id" />
        </el-select>
        <el-select v-model="statusFilter" clearable placeholder="全部状态">
          <el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" />
        </el-select>
        <el-date-picker
          v-model="dateRange"
          type="datetimerange"
          range-separator="至"
          start-placeholder="开始时间"
          end-placeholder="结束时间"
          :clearable="true"
        />
        <el-tooltip content="刷新">
          <el-button class="square-button" :loading="loading" aria-label="刷新" @click="loadFirst()">
            <RefreshCw v-if="!loading" :size="15" />
          </el-button>
        </el-tooltip>
      </div>

      <div
        ref="scrollContainer"
        v-loading="loading"
        class="task-log-list"
        @scroll="handleScroll"
      >
        <div v-for="run in items" :key="run.id" class="task-log-entry">
          <button class="task-log-entry-main" type="button" @click="toggleDetail(run)">
            <span class="task-log-expand">
              <ChevronDown v-if="expandedID === run.id" :size="15" />
              <ChevronRight v-else :size="15" />
            </span>
            <span class="task-log-icon" :class="run.status">
              <CircleCheck v-if="run.status === 'completed'" :size="16" />
              <CircleX v-else-if="['failed', 'completed_with_errors'].includes(run.status)" :size="16" />
              <Clock3 v-else :size="16" />
            </span>
            <span class="task-log-copy">
              <span class="task-log-title">
                <strong>{{ run.task_name }}</strong>
                <el-tag :type="statusTypes[run.status]" effect="plain" size="small">
                  {{ statusLabels[run.status] || run.status }}
                </el-tag>
              </span>
              <span class="task-log-meta">
                {{ actionLabels[run.action_type] || run.action_type }}
                · {{ run.trigger_type === "manual" ? "手动" : "定时" }}
                · {{ formatDate(run.created_at) }}
              </span>
              <span class="task-log-result" :class="{ failed: Number(run.failed_targets) > 0 }">
                共 {{ run.total_targets }} 个目标：成功 {{ run.success_targets }}，失败 {{ run.failed_targets }}
                <template v-if="duration(run)"> · 用时 {{ duration(run) }}</template>
              </span>
            </span>
          </button>

          <div v-if="expandedID === run.id" v-loading="detailLoading === run.id" class="task-log-targets">
            <div
              v-for="target in details[run.id]?.targets || []"
              :key="target.node_id + ':' + target.instance_id"
              class="task-log-target"
            >
              <span class="task-log-target-icon" :class="target.status">
                <TerminalSquare :size="14" />
              </span>
              <span>
                <strong>{{ target.instance_name }}</strong>
                <small>{{ target.node_name }} · {{ target.instance_id }}</small>
                <small v-if="target.error_message" class="task-log-error">
                  {{ target.error_code }} · {{ target.error_message }}
                </small>
              </span>
              <el-tag :type="statusTypes[target.status]" effect="plain" size="small">
                {{ statusLabels[target.status] || target.status }}
              </el-tag>
            </div>
          </div>
        </div>

        <div v-if="!loading && !items.length" class="task-log-empty">
          <Clock3 :size="24" /><span>暂无执行日志</span>
        </div>
        <div v-if="loadingMore" class="task-log-loading">正在加载更早的记录...</div>
        <div v-else-if="items.length && !hasMore" class="task-log-loading">已加载全部记录</div>
      </div>
    </div>
  </el-drawer>
</template>
