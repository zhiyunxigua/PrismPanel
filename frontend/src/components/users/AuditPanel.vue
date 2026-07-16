<script setup>
import { onMounted, reactive, ref } from "vue";
import { FileClock, RefreshCw, Search } from "lucide-vue-next";
import { ElMessage } from "element-plus";
import { request } from "../../api";

const loading = ref(false);
const items = ref([]);
const total = ref(0);
const selected = ref(null);
const detailOpen = ref(false);
const filters = reactive({ search: "", success: "", page: 1, pageSize: 20 });

async function load() {
  loading.value = true;
  const query = new URLSearchParams({ page: String(filters.page), page_size: String(filters.pageSize) });
  if (filters.search) query.set("search", filters.search);
  if (filters.success !== "") query.set("success", filters.success);
  try {
    const data = await request("/api/v1/audit?" + query);
    items.value = data.items;
    total.value = data.total;
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    loading.value = false;
  }
}

function search() {
  filters.page = 1;
  load();
}

function showDetail(row) {
  selected.value = row;
  detailOpen.value = true;
}

function formatDate(value) {
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  }).format(new Date(value));
}

const riskLabels = { normal: "普通", high: "高风险", critical: "严重" };
const riskTypes = { normal: "info", high: "warning", critical: "danger" };

onMounted(load);
</script>

<template>
  <div class="panel-stack">
    <div class="table-toolbar">
      <el-input v-model="filters.search" class="search-input" placeholder="搜索用户或资源" clearable @keyup.enter="search" @clear="search">
        <template #prefix><Search :size="16" /></template>
      </el-input>
      <el-select v-model="filters.success" class="status-filter" placeholder="全部结果" clearable @change="search">
        <el-option label="成功" value="true" /><el-option label="失败" value="false" />
      </el-select>
      <el-tooltip content="刷新"><el-button class="square-button" :loading="loading" aria-label="刷新" @click="load"><RefreshCw v-if="!loading" :size="16" /></el-button></el-tooltip>
    </div>
    <div class="table-frame">
      <el-table v-loading="loading" :data="items" row-key="id" @row-click="showDetail">
        <el-table-column label="时间" width="176"><template #default="{ row }">{{ formatDate(row.created_at) }}</template></el-table-column>
        <el-table-column label="操作人" min-width="150"><template #default="{ row }"><strong>{{ row.actor_display_name }}</strong><small class="block muted">{{ row.actor_username }}</small></template></el-table-column>
        <el-table-column label="动作" min-width="190"><template #default="{ row }"><code>{{ row.action }}</code></template></el-table-column>
        <el-table-column label="资源" min-width="170"><template #default="{ row }">{{ row.resource_name || row.resource_id || "-" }}</template></el-table-column>
        <el-table-column label="风险" width="90"><template #default="{ row }"><el-tag :type="riskTypes[row.risk_level]" effect="plain" size="small">{{ riskLabels[row.risk_level] }}</el-tag></template></el-table-column>
        <el-table-column label="结果" width="82"><template #default="{ row }"><el-tag :type="row.success ? 'success' : 'danger'" effect="plain" size="small">{{ row.success ? "成功" : "失败" }}</el-tag></template></el-table-column>
        <template #empty><div class="table-empty"><FileClock :size="24" /><span>暂无操作日志</span></div></template>
      </el-table>
    </div>
    <el-pagination v-if="total > filters.pageSize" v-model:current-page="filters.page" class="table-pagination" layout="total, prev, pager, next" :page-size="filters.pageSize" :total="total" @current-change="load" />
  </div>

  <el-drawer v-model="detailOpen" title="操作详情" size="min(520px, 94vw)">
    <el-descriptions v-if="selected" :column="1" border>
      <el-descriptions-item label="请求 ID"><code>{{ selected.request_id }}</code></el-descriptions-item>
      <el-descriptions-item label="动作"><code>{{ selected.action }}</code></el-descriptions-item>
      <el-descriptions-item label="操作人">{{ selected.actor_display_name }} ({{ selected.actor_username }})</el-descriptions-item>
      <el-descriptions-item label="来源 IP">{{ selected.source_ip }}</el-descriptions-item>
      <el-descriptions-item label="资源">{{ selected.resource_type }} / {{ selected.resource_id || "-" }}</el-descriptions-item>
      <el-descriptions-item label="结果">{{ selected.success ? "成功" : "失败 · " + selected.error_code }}</el-descriptions-item>
      <el-descriptions-item label="时间">{{ formatDate(selected.created_at) }}</el-descriptions-item>
    </el-descriptions>
    <pre v-if="selected?.detail" class="json-detail">{{ JSON.stringify(selected.detail, null, 2) }}</pre>
  </el-drawer>
</template>
