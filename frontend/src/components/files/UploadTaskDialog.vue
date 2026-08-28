<script setup>
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { File, FolderUp, RotateCcw, Trash2, Upload } from "lucide-vue-next";

const ROW_HEIGHT = 45;
const VIEWPORT_HEIGHT = 400;
const BUFFER_ROWS = 8;

const props = defineProps({
  visible: { type: Boolean, default: false },
  tasks: { type: Array, default: () => [] },
  stats: { type: Object, default: () => ({}) },
  scanning: { type: Boolean, default: false },
  preparing: { type: String, default: "" },
  uploading: { type: Boolean, default: false },
  pendingDirectories: { type: Number, default: 0 },
  currentTaskId: { type: String, default: "" },
});
const emit = defineEmits([
  "update:visible", "choose-files", "choose-directory", "start", "cancel", "cancel-all", "retry", "clear",
]);

const scrollTop = ref(0);
const viewport = ref(null);
let autoScrollFrame = 0;
const clock = ref(Date.now());
const clockTimer = window.setInterval(() => { clock.value = Date.now(); }, 1000);
onBeforeUnmount(() => {
  window.clearInterval(clockTimer);
  if (autoScrollFrame) window.cancelAnimationFrame(autoScrollFrame);
});

const startIndex = computed(() => Math.max(0, Math.floor(scrollTop.value / ROW_HEIGHT) - BUFFER_ROWS));
const visibleCount = Math.ceil(VIEWPORT_HEIGHT / ROW_HEIGHT) + BUFFER_ROWS * 2;
const visibleTasks = computed(() => props.tasks.slice(startIndex.value, startIndex.value + visibleCount));
const listOffset = computed(() => startIndex.value * ROW_HEIGHT);
const listHeight = computed(() => props.tasks.length * ROW_HEIGHT);
const waitingCount = computed(() => props.tasks.filter((task) => task.status === "waiting").length);
const canStart = computed(() => waitingCount.value > 0 || props.pendingDirectories > 0);
const completedCount = computed(() => props.tasks.filter((task) => terminalStatus(task.status)).length);
const failedCount = computed(() => props.tasks.filter((task) => task.status === "failed").length);
const successfulCount = computed(() => props.tasks.filter((task) => task.status === "done").length);
const totalBytes = computed(() => Number(props.stats.totalBytes) || props.tasks.reduce((sum, task) => sum + task.total, 0));
const loadedBytes = computed(() => Math.min(Number(props.stats.loadedBytes) || 0, totalBytes.value));
const totalPercentage = computed(() => totalBytes.value
  ? Math.min(100, loadedBytes.value / totalBytes.value * 100)
  : (props.tasks.length && completedCount.value === props.tasks.length ? 100 : 0));
const speed = computed(() => Number(props.stats.speed) || 0);
const elapsed = computed(() => props.stats.startedAt ? Math.max(0, clock.value - props.stats.startedAt) : 0);
const remaining = computed(() => speed.value > 0 ? Math.max(0, (totalBytes.value - loadedBytes.value) / speed.value * 1000) : 0);
const averageSpeed = computed(() => elapsed.value > 0 ? loadedBytes.value / (elapsed.value / 1000) : 0);
const hasStarted = computed(() => Boolean(props.stats.startedAt));

function terminalStatus(status) {
  return ["done", "failed", "skipped", "canceled"].includes(status);
}

function statusLabel(task) {
  return {
    waiting: "等待上传", uploading: `上传中(${Math.floor(taskPercent(task))}%)`, paused: "已暂停",
    done: "上传成功", failed: "上传失败", skipped: "已跳过", canceled: "已取消",
  }[task.status] || task.status;
}

function taskPercent(task) {
  if (!task.total) return task.status === "done" ? 100 : 0;
  return Math.min(100, task.loaded / task.total * 100);
}

function progressStyle(task) {
  return { width: `${taskPercent(task)}%` };
}

function formatSize(value) {
  let size = Number(value) || 0;
  const units = ["B", "KB", "MB", "GB", "TB"];
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(unit === 0 ? 0 : 2)} ${units[unit]}`;
}

function formatDuration(value) {
  if (!value || !Number.isFinite(value)) return "获取中";
  const seconds = Math.max(0, Math.round(value / 1000));
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor(seconds % 3600 / 60);
  const rest = seconds % 60;
  if (hours) return `${hours}小时${minutes}分钟`;
  if (minutes) return `${minutes}分钟${rest}秒`;
  return `${rest}秒`;
}

function choose(command) {
  emit(command === "directory" ? "choose-directory" : "choose-files");
}

function scheduleAutoScroll() {
  if (autoScrollFrame) window.cancelAnimationFrame(autoScrollFrame);
  autoScrollFrame = window.requestAnimationFrame(() => {
    autoScrollFrame = 0;
    scrollCurrentTaskIntoView();
  });
}

async function scrollCurrentTaskIntoView() {
  if (!props.visible || !props.currentTaskId) return;
  await nextTick();
  const element = viewport.value;
  const index = props.tasks.findIndex((task) => task.id === props.currentTaskId);
  if (!element || index < 0) return;
  const itemTop = index * ROW_HEIGHT;
  const itemBottom = itemTop + ROW_HEIGHT;
  const viewTop = element.scrollTop;
  const viewBottom = viewTop + element.clientHeight;
  if (itemTop < viewTop) {
    element.scrollTop = itemTop;
  } else if (itemBottom > viewBottom) {
    element.scrollTop = itemBottom - element.clientHeight;
  }
}

watch(() => [props.currentTaskId, props.visible, props.tasks.length], scheduleAutoScroll, { flush: "post" });
</script>

<template>
  <el-dialog
    :model-value="visible"
    title="上传文件"
    width="min(720px, 96vw)"
    class="upload-task-dialog"
    :close-on-click-modal="false"
    @update:model-value="emit('update:visible', $event)"
  >
    <div class="upload-toolbar">
      <el-dropdown split-button :disabled="uploading" @click="emit('choose-files')" @command="choose">
        <Upload :size="14" />上传文件
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item command="file"><Upload :size="14" />上传文件</el-dropdown-item>
            <el-dropdown-item command="directory"><FolderUp :size="14" />上传文件夹</el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
      <el-button :disabled="uploading || !tasks.length" @click="emit('clear')">
        <Trash2 :size="14" />清空列表
      </el-button>
    </div>

    <div v-if="scanning || preparing" class="upload-summary is-loading">
      <span>{{ preparing || "正在获取上传文件，请稍候..." }}</span>
    </div>
    <div v-else-if="uploading" class="upload-summary">
      <span>总进度：{{ totalPercentage.toFixed(2) }}%</span>
      <i />
      <span>正在上传：({{ completedCount }}/{{ tasks.length }})</span>
      <i />
      <span>上传速度：{{ formatSize(speed) }}/s</span>
      <i />
      <span>预计耗时：{{ formatDuration(remaining) }}</span>
    </div>
    <div v-else-if="hasStarted" class="upload-summary">
      <span>上传大小：{{ formatSize(loadedBytes) }}</span>
      <i />
      <span>共耗时：{{ formatDuration(elapsed) }}</span>
      <i />
      <span>平均速度：{{ formatSize(averageSpeed) }}/s</span>
      <i />
      <span>成功：{{ successfulCount }} 个</span>
      <span v-if="failedCount">失败：{{ failedCount }} 个</span>
    </div>

    <div v-if="!tasks.length" class="upload-empty">
      请将需要上传的文件/文件夹拖到此处
    </div>
    <div v-else class="upload-table">
      <div class="upload-head upload-row">
        <div class="file-name">文件名</div>
        <div>文件大小</div>
        <div>上传状态</div>
        <div class="operation">操作</div>
      </div>
      <div ref="viewport" class="upload-viewport" :style="{ height: `${VIEWPORT_HEIGHT}px` }" @scroll="scrollTop = $event.currentTarget.scrollTop">
        <div class="upload-spacer" :style="{ height: `${listHeight}px` }">
          <div class="upload-visible" :style="{ transform: `translateY(${listOffset}px)` }">
            <div
              v-for="task in visibleTasks"
              :key="task.id"
              class="upload-row upload-item"
              :class="`is-${task.status}`"
            >
              <div class="task-progress" :style="progressStyle(task)" />
              <div class="file-name" :title="task.displayPath || task.path">
                <File :size="17" />
                <span>{{ task.displayPath || task.path }}</span>
              </div>
              <div>{{ formatSize(task.total) }}</div>
              <div class="task-status" :title="task.error?.message || statusLabel(task)">
                {{ statusLabel(task) }}
              </div>
              <div class="operation">
                <el-button v-if="task.status === 'failed'" link @click="emit('retry', task)">
                  <RotateCcw :size="13" />重试
                </el-button>
                <el-button v-else-if="task.status === 'waiting'" link @click="emit('cancel', task)">取消</el-button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <el-button v-if="uploading" type="danger" @click="emit('cancel-all')">取消上传</el-button>
      <el-button
        v-if="!uploading && canStart"
        type="primary"
        :disabled="scanning || Boolean(preparing)"
        @click="emit('start')"
      >
        开始上传
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.upload-toolbar { display: flex; align-items: center; gap: 10px; margin-bottom: 16px; }
.upload-toolbar :deep(.el-button) { border-radius: 2px; }
.upload-toolbar svg { margin-right: 5px; vertical-align: -2px; }
.upload-summary { display: flex; min-height: 34px; align-items: center; gap: 10px; border: 1px solid #d9e7f5; margin-bottom: 12px; padding: 6px 12px; color: #555; background: #f4f8fc; font-size: 12px; }
.upload-summary i { width: 1px; height: 13px; background: #c9d8e6; }
.upload-summary.is-loading { color: #409eff; }
.upload-empty { display: grid; height: 445px; place-items: center; border: 1px solid #e5e7eb; color: #999; background: #fafafa; font-size: 13px; }
.upload-table { border: 1px solid #ddd; }
.upload-row { display: grid; grid-template-columns: minmax(0, 55fr) minmax(90px, 15fr) minmax(100px, 18fr) minmax(60px, 12fr); align-items: center; }
.upload-head { height: 44px; border-bottom: 1px solid #ddd; color: #555; background: #f5f5f5; font-size: 12px; font-weight: 600; }
.upload-head > div, .upload-item > div:not(.task-progress) { min-width: 0; padding: 0 10px; }
.upload-viewport { overflow-y: auto; overscroll-behavior: contain; }
.upload-spacer { position: relative; }
.upload-visible { position: absolute; inset: 0 0 auto; }
.upload-item { position: relative; height: 45px; border-bottom: 1px solid #eee; color: #666; background: #fff; font-size: 12px; overflow: hidden; }
.upload-item:hover { background: #f7fbff; }
.task-progress { position: absolute; z-index: 0; inset: 0 auto 0 0; pointer-events: none; background: rgba(89, 184, 110, .14); transition: width .12s linear; }
.upload-item.is-failed .task-progress { background: rgba(224, 82, 82, .12); }
.upload-item > div:not(.task-progress) { position: relative; z-index: 1; }
.file-name { display: flex; align-items: center; gap: 7px; overflow: hidden; }
.file-name svg { flex: 0 0 auto; color: #5c8dbc; }
.file-name span, .task-status { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.operation { display: flex; justify-content: flex-end; }
.operation :deep(.el-button) { height: 28px; padding: 0; font-size: 12px; }
.operation svg { margin-right: 3px; }
.is-done .task-status { color: #4b9b59; }
.is-failed .task-status { color: #d9534f; }
@media (max-width: 640px) {
  .upload-summary { flex-wrap: wrap; }
  .upload-row { grid-template-columns: minmax(0, 1fr) 82px 88px; }
  .upload-row > .operation { display: none; }
}
</style>
