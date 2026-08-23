<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ArrowLeft, Cpu, Edit3, MemoryStick, RefreshCw, Server, ShieldAlert, ShieldCheck, Trash2 } from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { request } from "../api";
import { hasPermission } from "../session";
import MetricLineChart from "../components/metrics/MetricLineChart.vue";

const route = useRoute();
const router = useRouter();
const loading = ref(false);
const submitting = ref(false);
const node = ref(null);
const metricPoints = ref([]);
const activeTab = ref("overview");
const dialogOpen = ref(false);
const formRef = ref();
const form = reactive({ name: "", base_url: "", public_url: "", token: "", enabled: true });
const canUpdate = computed(() => hasPermission("node.update"));
const canDelete = computed(() => hasPermission("node.delete"));
const currentMetrics = computed(() => metricPoints.value.at(-1) || null);
const cpuPoints = computed(() => metricPoints.value.map((point) => ({
  sampled_at: point.sampled_at, value: point.cpu_percent,
})));
const memoryPoints = computed(() => metricPoints.value.map((point) => ({
  sampled_at: point.sampled_at, value: Number(point.memory_used_bytes) / 1024 / 1024 / 1024,
})));
const highestCorePercent = computed(() => {
  const values = currentMetrics.value?.cpu_core_percent || [];
  return values.length ? Math.max(...values) : null;
});
let refreshTimer;
const rules = {
  name: [{ required: true, message: "请输入节点名称", trigger: "blur" }],
  base_url: [
    { required: true, message: "请输入连接地址", trigger: "blur" },
    { pattern: /^https?:\/\/[^\s]+$/i, message: "请输入完整的 HTTP 或 HTTPS URL", trigger: "blur" },
  ],
  public_url: [{ validator: (_rule, value, callback) => {
    if (!value || /^https?:\/\/[^\s]+$/i.test(value)) callback();
    else callback(new Error("请输入完整的 HTTP 或 HTTPS URL"));
  }, trigger: "blur" }],
};
const statusLabels = { CONNECTING: "连接中", ONLINE: "在线", DEGRADED: "能力异常", OFFLINE: "离线", DISABLED: "已禁用" };

async function load(silent = false) {
  if (!silent) loading.value = true;
  try {
    const [nodeData, metricData] = await Promise.all([
      request("/api/v1/nodes/" + route.params.id),
      request("/api/v1/nodes/" + route.params.id + "/metrics").catch(() => null),
    ]);
    node.value = nodeData;
    if (metricData) metricPoints.value = metricData.points || [];
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) loading.value = false;
  }
}

function openEdit() {
  Object.assign(form, {
    name: node.value.name, base_url: node.value.base_url,
    public_url: node.value.public_url || "", token: "", enabled: node.value.enabled,
  });
  dialogOpen.value = true;
}

async function save() {
  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) return;
  submitting.value = true;
  try {
    node.value = await request("/api/v1/nodes/" + route.params.id, {
      method: "PUT", body: JSON.stringify(form),
    });
    dialogOpen.value = false;
    ElMessage.success("节点设置已更新");
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

async function remove() {
  try {
    const result = await ElMessageBox.prompt(
      "删除节点会立即断开管理连接，历史审计仍会保留。",
      "删除节点",
      {
        type: "error", inputPlaceholder: "输入 " + node.value.name + " 确认",
        inputValidator: (value) => value === node.value.name || "节点名称不匹配",
        confirmButtonText: "删除", cancelButtonText: "取消",
      },
    );
    if (result.value !== node.value.name) return;
    await request("/api/v1/nodes/" + route.params.id, { method: "DELETE" });
    ElMessage.success("节点已删除");
    await router.replace({ name: "nodes" });
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message);
  }
}

function formatDate(value) {
  if (!value) return "从未";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  }).format(new Date(value));
}

function percent(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number.toFixed(1) + "%" : "--";
}

function memory(value) {
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
  <div v-loading="loading" class="content-stack">
    <div class="page-toolbar detail-toolbar">
      <div class="detail-title">
        <el-tooltip content="返回节点列表"><button class="icon-control" type="button" aria-label="返回节点列表" @click="router.push({ name: 'nodes' })"><ArrowLeft :size="18" /></button></el-tooltip>
        <span class="node-symbol"><Server :size="18" /></span>
        <div><h2>{{ node?.name || "节点" }}</h2><p>{{ node?.daemon_id || "等待连接" }}</p></div>
      </div>
      <div class="toolbar-actions">
        <el-tooltip content="刷新"><el-button class="square-button" :loading="loading" aria-label="刷新" @click="load"><RefreshCw v-if="!loading" :size="16" /></el-button></el-tooltip>
        <el-button v-if="canUpdate" @click="openEdit"><Edit3 :size="16" />设置</el-button>
        <el-button v-if="canDelete" type="danger" plain @click="remove"><Trash2 :size="16" />删除</el-button>
      </div>
    </div>

    <el-alert v-if="node?.security_level === 'unencrypted'" type="warning" :closable="false" show-icon>
      <template #title><span class="alert-title"><ShieldAlert :size="16" />当前管理连接未加密</span></template>
    </el-alert>

    <el-tabs v-model="activeTab" class="management-tabs">
      <el-tab-pane label="概览" name="overview">
        <div class="detail-summary">
          <div><span>状态</span><strong><span v-if="node" class="node-status" :class="node.status.toLowerCase()">{{ statusLabels[node.status] }}</span></strong></div>
          <div><span>延迟</span><strong>{{ node?.status === "ONLINE" ? node.latency_ms + " ms" : "-" }}</strong></div>
          <div><span>daemon 版本</span><strong>{{ node?.daemon_version || "未知" }}</strong></div>
          <div><span>协议版本</span><strong>{{ node?.protocol_version || "未知" }}</strong></div>
        </div>
        <section class="data-section">
          <div class="section-title"><div><h3>连接</h3><p>当前面板保存的节点连接设置</p></div></div>
          <el-descriptions v-if="node" :column="2" border class="detail-descriptions">
            <el-descriptions-item label="连接 URL"><code>{{ node.base_url }}</code></el-descriptions-item>
            <el-descriptions-item label="公网 URL"><code>{{ node.public_url || node.reported_public_url || "未配置" }}</code></el-descriptions-item>
            <el-descriptions-item label="最后连接">{{ formatDate(node.last_connected_at) }}</el-descriptions-item>
            <el-descriptions-item label="最近错误">{{ node.last_error || "无" }}</el-descriptions-item>
          </el-descriptions>
        </section>
        <section class="data-section">
          <div class="section-title"><div><h3>能力</h3><p>守护进程当前声明的协议能力</p></div></div>
          <div class="capability-list"><el-tag v-for="capability in node?.capabilities || []" :key="capability" effect="plain">{{ capability }}</el-tag><span v-if="!node?.capabilities?.length" class="muted">未上报</span></div>
        </section>
      </el-tab-pane>
      <el-tab-pane label="性能" name="performance">
        <div class="detail-summary host-metric-summary">
          <div><span>主机 CPU</span><strong><Cpu :size="15" />{{ percent(currentMetrics?.cpu_percent) }}</strong></div>
          <div><span>主机内存</span><strong><MemoryStick :size="15" />{{ memory(currentMetrics?.memory_used_bytes) }} / {{ memory(currentMetrics?.memory_total_bytes) }}</strong></div>
          <div><span>最高负载核心</span><strong>{{ percent(highestCorePercent) }}</strong></div>
          <div><span>可用内存</span><strong>{{ memory(currentMetrics?.memory_available_bytes) }}</strong></div>
        </div>
        <div class="metric-chart-grid">
          <MetricLineChart title="主机 CPU" :points="cpuPoints" unit="%" :maximum="100" color="#397eaf" />
          <MetricLineChart title="主机内存" :points="memoryPoints" unit=" GB" :decimals="2" color="#2d8a60" />
        </div>
        <section class="data-section core-metric-section">
          <div class="section-title"><div><h3>逻辑核心</h3><p>{{ currentMetrics?.cpu_core_percent?.length || 0 }} 个逻辑核心的当前占用</p></div></div>
          <div v-if="currentMetrics?.cpu_core_percent?.length" class="core-metric-grid">
            <div v-for="(value, index) in currentMetrics.cpu_core_percent" :key="index" class="core-metric">
              <div><span>CPU {{ index + 1 }}</span><strong>{{ percent(value) }}</strong></div>
              <el-progress :percentage="Math.min(100, Math.max(0, Number(value) || 0))" :show-text="false" :stroke-width="5" />
            </div>
          </div>
          <div v-else class="empty-state"><strong>正在等待主机性能数据</strong></div>
        </section>
      </el-tab-pane>
      <el-tab-pane label="实例" name="instances">
        <div class="settings-action">
          <el-button @click="router.push({ name: 'servers', query: { node_id: route.params.id } })">
            <Server :size="16" />查看本节点服务器
          </el-button>
        </div>
      </el-tab-pane>
      <el-tab-pane label="网络白名单" name="firewall">
        <div class="settings-action">
          <el-button @click="router.push({ name: 'firewall', query: { node_id: route.params.id } })">
            <ShieldCheck :size="16" />管理本节点网络白名单
          </el-button>
        </div>
      </el-tab-pane>
      <el-tab-pane label="终端" name="terminal"><div class="empty-state"><strong>节点终端模块尚未接入</strong></div></el-tab-pane>
      <el-tab-pane label="连接与设置" name="settings"><div class="settings-action"><el-button v-if="canUpdate" @click="openEdit"><Edit3 :size="16" />编辑节点设置</el-button></div></el-tab-pane>
      <el-tab-pane label="操作记录" name="audit"><div class="empty-state"><strong>请在用户页的操作日志中按节点筛选</strong></div></el-tab-pane>
    </el-tabs>
  </div>

  <el-dialog v-model="dialogOpen" title="节点设置" width="600px">
    <el-form ref="formRef" :model="form" :rules="rules" label-position="top">
      <div class="dialog-form-grid">
        <el-form-item label="节点名称" prop="name"><el-input v-model="form.name" maxlength="100" /></el-form-item>
        <el-form-item label="连接状态"><el-switch v-model="form.enabled" active-text="启用" inactive-text="禁用" /></el-form-item>
      </div>
      <el-form-item label="连接 URL" prop="base_url"><el-input v-model="form.base_url" /></el-form-item>
      <el-form-item label="新节点令牌"><el-input v-model="form.token" type="password" show-password placeholder="留空表示不修改" autocomplete="off" /></el-form-item>
      <el-form-item label="公网 URL" prop="public_url"><el-input v-model="form.public_url" placeholder="可选" /></el-form-item>
    </el-form>
    <template #footer><el-button @click="dialogOpen = false">取消</el-button><el-button type="primary" :loading="submitting" @click="save">保存</el-button></template>
  </el-dialog>
</template>
