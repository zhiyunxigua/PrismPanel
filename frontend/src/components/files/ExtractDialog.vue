<script setup>
import { computed, reactive, watch } from "vue";
import { FileArchive } from "lucide-vue-next";

const props = defineProps({
  visible: { type: Boolean, default: false },
  entry: { type: Object, default: null },
  task: { type: Object, default: null },
});
const emit = defineEmits(["update:visible", "start"]);
const form = reactive({
  destination: ".", password: "", encoding: "utf-8", directoryMode: "755", conflictPolicy: "overwrite",
});

watch(() => [props.visible, props.entry?.path], () => {
  if (!props.visible || !props.entry) return;
  form.destination = defaultSameNamePath(props.entry.path);
  form.password = "";
  form.encoding = "utf-8";
  form.directoryMode = "755";
  form.conflictPolicy = "overwrite";
}, { immediate: true });

const running = computed(() => props.task && ["pending", "running"].includes(props.task.status));
const done = computed(() => props.task?.status === "done");
const failed = computed(() => props.task?.status === "failed");
const destination = computed(() => normalizePath(form.destination));
const validDirectoryMode = computed(() => /^[0-7]{3}$/.test(form.directoryMode));
const progress = computed(() => {
  if (!props.task) return 0;
  if (props.task.bytes_total) return Math.round(Math.min(props.task.bytes_done, props.task.bytes_total) / props.task.bytes_total * 100);
  if (props.task.files_total) return Math.round(Math.min(props.task.files_done, props.task.files_total) / props.task.files_total * 100);
  return done.value ? 100 : 0;
});

function start() {
  if (!destination.value || !validDirectoryMode.value) return;
  emit("start", {
    destination: destination.value,
    password: form.password,
    encoding: form.encoding,
    directoryMode: form.directoryMode,
    conflictPolicy: form.conflictPolicy,
  });
}

function defaultSameNamePath(path) {
  const parts = String(path || "archive.zip").split("/");
  const name = parts.pop().replace(/\.zip$/i, "") || "archive";
  return [...parts, name].filter(Boolean).join("/") || name;
}

function normalizePath(value) {
  return String(value || "").trim().replaceAll("\\", "/").replace(/^\/+|\/+$/g, "") || ".";
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    title="解压 ZIP"
    width="min(620px, 94vw)"
    :show-close="!running"
    :close-on-click-modal="!running"
    :close-on-press-escape="!running"
    @update:model-value="emit('update:visible', $event)"
  >
    <div class="extract-source"><FileArchive :size="20" /><span>{{ entry?.path }}</span></div>
    <template v-if="!task">
      <el-form label-position="left" label-width="96px" class="extract-form">
        <el-form-item label="解压到">
          <el-input v-model="form.destination" placeholder="相对于工作目录的路径" />
        </el-form-item>
        <el-form-item label="解压密码">
          <el-input v-model="form.password" type="password" show-password autocomplete="new-password" placeholder="无密码则留空" />
        </el-form-item>
        <el-form-item label="解压编码">
          <el-select v-model="form.encoding">
            <el-option label="UTF-8" value="utf-8" />
            <el-option label="GBK" value="gbk" />
          </el-select>
        </el-form-item>
        <el-form-item label="目录权限" :error="validDirectoryMode ? '' : '请输入 000 到 777 的三位八进制权限'">
          <el-input v-model="form.directoryMode" maxlength="3" inputmode="numeric" placeholder="755" />
        </el-form-item>
        <el-form-item label="同名文件处理">
          <el-select v-model="form.conflictPolicy">
            <el-option label="覆盖同名文件" value="overwrite" />
            <el-option label="跳过同名文件" value="skip" />
            <el-option label="自动重命名" value="rename" />
          </el-select>
        </el-form-item>
      </el-form>
    </template>
    <div v-else class="extract-progress">
      <div><span>{{ task.message }}</span><strong>{{ progress }}%</strong></div>
      <el-progress :percentage="progress" :status="failed ? 'exception' : done ? 'success' : ''" />
      <dl>
        <dt>当前文件</dt><dd>{{ task.current_file || "-" }}</dd>
        <dt>文件进度</dt><dd>{{ task.files_done }}/{{ task.files_total }}</dd>
        <dt>已跳过</dt><dd>{{ task.skipped || 0 }}</dd>
      </dl>
      <div v-if="failed" class="extract-error">
        <strong>{{ task.error?.message || "解压失败" }}</strong>
        <pre v-if="task.error?.details?.length">{{ task.error.details.join('\n') }}</pre>
      </div>
    </div>
    <template #footer>
      <el-button :disabled="running" @click="emit('update:visible', false)">{{ done || failed ? "关闭" : "取消" }}</el-button>
      <el-button v-if="!task" type="primary" :disabled="!destination || !validDirectoryMode" @click="start">开始解压</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.extract-source { display: flex; align-items: center; gap: 9px; margin-bottom: 18px; border-bottom: 1px solid var(--app-border-soft); padding-bottom: 12px; color: var(--app-text); font-size: 13px; }
.extract-source svg { color: #9a6b32; }
.extract-source span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.extract-form :deep(.el-select) { width: 100%; }
.extract-progress { display: grid; gap: 12px; }
.extract-progress > div:first-child { display: flex; justify-content: space-between; color: var(--app-text-secondary); font-size: 12px; }
.extract-progress dl { display: grid; grid-template-columns: 80px minmax(0, 1fr); gap: 7px 12px; margin: 0; font-size: 12px; }
.extract-progress dt { color: var(--app-text-muted); }
.extract-progress dd { min-width: 0; margin: 0; overflow: hidden; color: var(--app-text); text-overflow: ellipsis; white-space: nowrap; }
.extract-error { border-left: 3px solid #b83c35; padding: 9px 11px; color: #8b3934; background: #fff5f3; }
.extract-error pre { margin: 8px 0 0; font: 11px/1.6 Consolas, monospace; white-space: pre-wrap; }
</style>
