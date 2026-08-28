<script setup>
import { computed, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { ShieldCheck } from "lucide-vue-next";
import { request } from "../../api";

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  nodeId: { type: String, default: "" },
  instance: { type: Object, default: null },
  submitting: { type: Boolean, default: false },
});
const emit = defineEmits(["update:modelValue", "updated"]);

const loading = ref(false);
const saving = ref(false);
const users = ref([]);
const selected = ref([]);
const original = ref([]);

const open = computed({
  get: () => props.modelValue,
  set: (value) => emit("update:modelValue", value),
});
const title = computed(() => `${props.instance?.name || "实例"} · 实例管理员`);
const changed = computed(() => JSON.stringify([...selected.value].sort()) !== JSON.stringify([...original.value].sort()));

async function load() {
  if (!props.nodeId || !props.instance?.instance_id) return;
  loading.value = true;
  try {
    const [userData, adminData] = await Promise.all([
      request("/api/v1/users?status=active&page=1&page_size=100"),
      request(`/api/v1/instances/${encodeURIComponent(props.instance.instance_id)}/admins?node_id=${encodeURIComponent(props.nodeId)}`),
    ]);
    users.value = userData.items || [];
    selected.value = (adminData.admins || []).map((item) => item.user_id);
    original.value = [...selected.value];
  } catch (error) {
    ElMessage.error("读取实例管理员失败：" + error.message);
  } finally {
    loading.value = false;
  }
}

function userLabel(user) {
  return user.display_name && user.display_name !== user.username
    ? `${user.display_name} (${user.username})`
    : user.username;
}

async function save() {
  if (!props.nodeId || !props.instance?.instance_id || saving.value) return;
  saving.value = true;
  try {
    const data = await request(
      `/api/v1/instances/${encodeURIComponent(props.instance.instance_id)}/admins?node_id=${encodeURIComponent(props.nodeId)}`,
      { method: "PUT", body: JSON.stringify({ user_ids: selected.value }) },
    );
    original.value = [...selected.value];
    ElMessage.success("实例管理员已更新");
    emit("updated", data.admins || []);
    open.value = false;
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    saving.value = false;
  }
}

watch(() => [props.modelValue, props.instance?.instance_id, props.nodeId], ([visible]) => {
  if (visible) void load();
});
</script>

<template>
  <el-dialog
    v-model="open"
    :title="title"
    width="520px"
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
  >
    <div v-loading="loading" class="instance-admin-content">
      <p class="instance-admin-hint">被分配的用户可以管理该实例的启动、控制台、文件、玩家和插件。</p>
      <el-select
        v-model="selected"
        class="full-control"
        multiple
        filterable
        collapse-tags
        collapse-tags-tooltip
        clearable
        placeholder="选择用户"
        :disabled="loading || saving"
      >
        <el-option v-for="user in users" :key="user.id" :label="userLabel(user)" :value="user.id" />
      </el-select>
      <div v-if="!users.length && !loading" class="instance-admin-empty">暂无可分配的启用用户</div>
    </div>
    <template #footer>
      <el-button :disabled="saving" @click="open = false">取消</el-button>
      <el-button type="primary" :loading="saving" :disabled="loading || !changed" @click="save">
        <ShieldCheck :size="15" />保存
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.instance-admin-content { min-height: 88px; }
.instance-admin-hint { margin: 0 0 14px; color: var(--app-text-muted); font-size: 12px; line-height: 1.6; }
.instance-admin-empty { padding: 24px 0 8px; color: var(--app-text-muted); font-size: 12px; text-align: center; }
</style>
