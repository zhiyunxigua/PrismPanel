<script setup>
import { onBeforeUnmount, reactive, ref } from "vue";

const visible = ref(false);
const state = reactive({
  title: "目标已存在",
  message: "",
  detail: "",
  allowOverwriteAll: false,
});
let resolver = null;

function ask(options) {
  if (resolver) resolver("skip");
  Object.assign(state, {
    title: options.title || "目标已存在",
    message: options.message || "",
    detail: options.detail || "",
    allowOverwriteAll: Boolean(options.allowOverwriteAll),
  });
  visible.value = true;
  return new Promise((resolve) => {
    resolver = resolve;
  });
}

function decide(action) {
  visible.value = false;
  const resolve = resolver;
  resolver = null;
  resolve?.(action);
}

onBeforeUnmount(() => {
  resolver?.("skip");
  resolver = null;
});

defineExpose({ ask });
</script>

<template>
  <el-dialog
    v-model="visible"
    :title="state.title"
    width="min(500px, 94vw)"
    :show-close="false"
    :close-on-click-modal="false"
    :close-on-press-escape="false"
  >
    <div class="conflict-content">
      <p>{{ state.message }}</p>
      <small v-if="state.detail">{{ state.detail }}</small>
    </div>
    <template #footer>
      <el-button @click="decide('skip')">跳过</el-button>
      <el-button type="primary" plain @click="decide('overwrite')">覆盖</el-button>
      <el-button v-if="state.allowOverwriteAll" type="primary" @click="decide('overwrite-all')">
        全部覆盖
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.conflict-content { display: grid; gap: 8px; }
.conflict-content p { margin: 0; color: #303a34; line-height: 1.65; }
.conflict-content small { color: #778179; line-height: 1.5; }
</style>
