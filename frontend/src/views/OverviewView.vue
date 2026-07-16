<script setup>
import { onBeforeUnmount, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { Activity, AlertTriangle, Boxes, RefreshCw, Server, ShieldAlert } from "lucide-vue-next";
import { ElMessage } from "element-plus";
import { request } from "../api";

const router = useRouter();
const loading = ref(false);
const dashboard = ref({ summary: {}, nodes: [] });
let refreshTimer;
const statusLabels = { CONNECTING: "连接中", ONLINE: "在线", DEGRADED: "能力异常", OFFLINE: "离线", DISABLED: "已禁用" };

async function refresh(silent = false) {
  if (!silent) loading.value = true;
  try {
    dashboard.value = await request("/api/v1/dashboard");
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) loading.value = false;
  }
}

function formatDate(value) {
  if (!value) return "从未连接";
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

function highestCore(node) {
  const cores = node.metrics?.cpu_core_percent || [];
  return cores.length ? percent(Math.max(...cores)) : "--";
}

onMounted(() => {
  refresh();
  refreshTimer = window.setInterval(() => refresh(true), 5000);
});
onBeforeUnmount(() => window.clearInterval(refreshTimer));
</script>

<template>
  <div class="content-stack">
    <div class="page-toolbar">
      <div><h2>运行概况</h2><p>当前控制面的节点连接状态</p></div>
      <el-tooltip content="刷新"><el-button class="square-button" :loading="loading" aria-label="刷新" @click="refresh"><RefreshCw v-if="!loading" :size="16" /></el-button></el-tooltip>
    </div>

    <section class="summary-grid" aria-label="全局摘要">
      <article class="summary-item"><div class="summary-icon players"><Activity :size="19" /></div><div><span>在线玩家</span><strong>{{ dashboard.summary.online_players ?? 0 }}</strong></div></article>
      <article class="summary-item"><div class="summary-icon nodes"><Server :size="19" /></div><div><span>在线节点</span><strong>{{ dashboard.summary.online_nodes ?? 0 }}</strong></div></article>
      <article class="summary-item"><div class="summary-icon instances"><Boxes :size="19" /></div><div><span>运行实例</span><strong>{{ dashboard.summary.running_instances ?? 0 }}</strong></div></article>
      <article class="summary-item"><div class="summary-icon alerts"><AlertTriangle :size="19" /></div><div><span>当前告警</span><strong>{{ dashboard.summary.active_alerts ?? 0 }}</strong></div></article>
    </section>

    <section class="overview-nodes" v-loading="loading">
      <button v-for="node in dashboard.nodes" :key="node.id" class="overview-node" type="button" @click="router.push({ name: 'node-detail', params: { id: node.id } })">
        <div class="overview-node-head">
          <div class="node-cell"><span class="node-symbol"><Server :size="18" /></span><div><strong>{{ node.name }}</strong><small>{{ node.daemon_id }}</small></div></div>
          <div class="node-head-status"><span class="node-status" :class="node.status.toLowerCase()">{{ statusLabels[node.status] }}</span><span>{{ node.latency_ms || 0 }} ms</span><span v-if="node.security_level === 'unencrypted'" class="security-warning"><ShieldAlert :size="13" />HTTP</span></div>
        </div>
        <div class="overview-node-metrics">
          <div><span>CPU</span><strong>{{ percent(node.metrics?.cpu_percent) }}</strong></div>
          <div><span>内存</span><strong>{{ gigabytes(node.metrics?.memory_used_bytes) }} / {{ gigabytes(node.metrics?.memory_total_bytes) }}</strong></div>
          <div><span>逻辑核心</span><strong>{{ node.metrics?.cpu_core_percent?.length || "--" }}</strong></div>
          <div><span>最高核心</span><strong>{{ highestCore(node) }}</strong></div>
          <div><span>daemon</span><strong>{{ node.daemon_version || "未知" }}</strong></div>
          <div><span>最后连接</span><strong>{{ formatDate(node.last_connected_at) }}</strong></div>
        </div>
      </button>
      <div v-if="!loading && !dashboard.nodes.length" class="empty-state"><Server :size="26" /><strong>暂无可查看的节点</strong></div>
    </section>
  </div>
</template>
