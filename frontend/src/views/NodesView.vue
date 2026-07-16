<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { AlertTriangle, CheckCircle2, Plus, RefreshCw, Server, ShieldAlert } from "lucide-vue-next";
import { ElMessage } from "element-plus";
import { request } from "../api";
import { hasPermission } from "../session";

const router = useRouter();
const loading = ref(false);
const submitting = ref(false);
const testing = ref(false);
const nodes = ref([]);
const dialogOpen = ref(false);
const testResult = ref(null);
const formRef = ref();
const form = reactive({ name: "", base_url: "", public_url: "", token: "", enabled: true });
const canCreate = computed(() => hasPermission("node.create"));
let refreshTimer;
const connectionKey = computed(() => form.base_url.trim() + "\n" + form.token.trim());
const rules = {
  name: [{ required: true, message: "请输入节点名称", trigger: "blur" }],
  base_url: [
    { required: true, message: "请输入连接地址", trigger: "blur" },
    { pattern: /^https?:\/\/[^\s]+$/i, message: "请输入完整的 HTTP 或 HTTPS URL", trigger: "blur" },
  ],
  token: [{ required: true, message: "请输入节点令牌", trigger: "blur" }],
  public_url: [{ validator: (_rule, value, callback) => {
    if (!value || /^https?:\/\/[^\s]+$/i.test(value)) callback();
    else callback(new Error("请输入完整的 HTTP 或 HTTPS URL"));
  }, trigger: "blur" }],
};

watch(connectionKey, () => {
  testResult.value = null;
});

async function load(silent = false) {
  if (!silent) loading.value = true;
  try {
    const data = await request("/api/v1/nodes");
    nodes.value = data.items;
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) loading.value = false;
  }
}

function openCreate() {
  Object.assign(form, { name: "", base_url: "http://", public_url: "", token: "", enabled: true });
  testResult.value = null;
  dialogOpen.value = true;
}

async function testConnection() {
  const valid = await formRef.value?.validateField(["base_url", "token"]).then(() => true).catch(() => false);
  if (!valid) return;
  testing.value = true;
  try {
    testResult.value = await request("/api/v1/nodes/test", {
      method: "POST", body: JSON.stringify({ base_url: form.base_url, token: form.token }),
    });
    ElMessage.success("连接成功");
  } catch (error) {
    testResult.value = null;
    ElMessage.error(error.message);
  } finally {
    testing.value = false;
  }
}

async function createNode() {
  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) return;
  submitting.value = true;
  try {
    await request("/api/v1/nodes", { method: "POST", body: JSON.stringify(form) });
    dialogOpen.value = false;
    ElMessage.success("节点配置已保存");
    await load();
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

const statusLabels = { CONNECTING: "连接中", ONLINE: "在线", DEGRADED: "能力异常", OFFLINE: "离线", DISABLED: "已禁用" };
function formatDate(value) {
  if (!value) return "从未";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
  }).format(new Date(value));
}

function percent(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number.toFixed(1) + "%" : "--";
}

function gigabytes(value) {
  const number = Number(value);
  return Number.isFinite(number) ? (number / 1024 / 1024 / 1024).toFixed(2) + " GB" : "--";
}

onMounted(() => {
  load();
  refreshTimer = window.setInterval(() => load(true), 5000);
});
onBeforeUnmount(() => window.clearInterval(refreshTimer));
</script>

<template>
  <div class="content-stack">
    <div class="page-toolbar">
      <div><h2>节点</h2><p>{{ nodes.length }} 个守护进程节点</p></div>
      <div class="toolbar-actions">
        <el-tooltip content="刷新"><el-button class="square-button" :loading="loading" aria-label="刷新" @click="load"><RefreshCw v-if="!loading" :size="16" /></el-button></el-tooltip>
        <el-button v-if="canCreate" type="primary" @click="openCreate"><Plus :size="16" />新增节点</el-button>
      </div>
    </div>
    <div class="table-frame">
      <el-table v-loading="loading" :data="nodes" row-key="id" @row-click="(row) => router.push({ name: 'node-detail', params: { id: row.id } })">
        <el-table-column label="节点" min-width="210"><template #default="{ row }"><div class="node-cell"><span class="node-symbol"><Server :size="17" /></span><div><strong>{{ row.name }}</strong><small>{{ row.daemon_id || "等待连接" }}</small></div></div></template></el-table-column>
        <el-table-column label="状态" width="110"><template #default="{ row }"><span class="node-status" :class="row.status.toLowerCase()">{{ statusLabels[row.status] }}</span></template></el-table-column>
        <el-table-column label="地址" min-width="230"><template #default="{ row }"><code>{{ row.base_url }}</code><span v-if="row.security_level === 'unencrypted'" class="security-warning"><ShieldAlert :size="13" />未加密</span></template></el-table-column>
        <el-table-column label="延迟" width="90"><template #default="{ row }">{{ row.status === "ONLINE" ? row.latency_ms + " ms" : "-" }}</template></el-table-column>
        <el-table-column label="协议 / 版本" min-width="140"><template #default="{ row }"><strong>{{ row.protocol_version || "-" }}</strong><small class="block muted">{{ row.daemon_version || "未知" }}</small></template></el-table-column>
        <el-table-column label="性能" min-width="190"><template #default="{ row }"><strong>{{ percent(row.metrics?.cpu_percent) }}</strong><small class="block muted">{{ gigabytes(row.metrics?.memory_used_bytes) }} / {{ gigabytes(row.metrics?.memory_total_bytes) }}</small></template></el-table-column>
        <el-table-column label="最后连接" min-width="130"><template #default="{ row }">{{ formatDate(row.last_connected_at) }}</template></el-table-column>
        <template #empty><div class="table-empty"><Server :size="25" /><span>暂无节点</span></div></template>
      </el-table>
    </div>
  </div>

  <el-dialog v-model="dialogOpen" title="新增节点" width="600px">
    <el-form ref="formRef" :model="form" :rules="rules" label-position="top">
      <el-form-item label="节点名称" prop="name"><el-input v-model="form.name" maxlength="100" /></el-form-item>
      <el-form-item label="连接 URL" prop="base_url"><el-input v-model="form.base_url" placeholder="https://node.example.com:24444" /></el-form-item>
      <el-form-item label="节点令牌" prop="token"><el-input v-model="form.token" type="password" show-password autocomplete="off" /></el-form-item>
      <el-form-item label="公网 URL" prop="public_url"><el-input v-model="form.public_url" placeholder="可选" /></el-form-item>
      <div class="connection-test-row">
        <el-button :loading="testing" @click="testConnection">测试连接</el-button>
        <span v-if="testResult" class="test-success"><CheckCircle2 :size="16" />{{ testResult.daemon_version }} · {{ testResult.latency_ms }} ms</span>
        <span v-else class="muted"><AlertTriangle :size="15" />未测试</span>
      </div>
    </el-form>
    <template #footer><el-button @click="dialogOpen = false">取消</el-button><el-button type="primary" :loading="submitting" @click="createNode">保存</el-button></template>
  </el-dialog>
</template>
