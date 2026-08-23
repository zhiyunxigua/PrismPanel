<script setup>
import { computed, onMounted, ref } from "vue";
import { Laptop, RefreshCw, Upload } from "lucide-vue-next";
import { ElMessage } from "element-plus";
import { request } from "../api";

const loading = ref(false);
const publishing = ref(false);
const publishOpen = ref(false);
const releases = ref([]);
const bundle = ref(null);
const notes = ref("");

const latest = computed(() => releases.value[0] || null);

onMounted(load);

async function load(silent = false) {
  if (!silent) loading.value = true;
  try {
    const result = await request("/api/v1/winapp/releases");
    releases.value = result.items || [];
  } catch (error) {
    if (!silent) ElMessage.error(error.message || "加载 WinApp 版本失败");
  } finally {
    if (!silent) loading.value = false;
  }
}

function selectBundle(file) { bundle.value = file.raw || null; }
function clearBundle() { bundle.value = null; }
function resetPublish() {
  bundle.value = null;
  notes.value = "";
}

async function publishRelease() {
  if (!bundle.value) {
    ElMessage.warning("请选择构建生成的 WinApp 发布 ZIP");
    return;
  }
  publishing.value = true;
  try {
    const body = new FormData();
    body.append("bundle", bundle.value);
    body.append("notes", notes.value.trim());
    const release = await request("/api/v1/winapp/releases", { method: "POST", body });
    publishOpen.value = false;
    resetPublish();
    ElMessage.success(`WinApp ${release.version} 已发布`);
    await load(true);
  } catch (error) {
    ElMessage.error(error.message || "发布 WinApp 版本失败");
  } finally {
    publishing.value = false;
  }
}

function formatDate(value) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
  }).format(new Date(value));
}

function fileSize(value) {
  const bytes = Number(value);
  if (!Number.isFinite(bytes)) return "-";
  return (bytes / 1024 / 1024).toFixed(2) + " MiB";
}
</script>

<template>
  <div class="content-stack">
    <div class="page-toolbar">
      <div><h2>客户端更新</h2><p>{{ latest ? `当前发布 ${latest.version}` : "尚未发布 WinApp 版本" }}</p></div>
      <div class="toolbar-actions">
        <el-tooltip content="刷新">
          <el-button class="square-button" :loading="loading" aria-label="刷新" @click="load()">
            <RefreshCw v-if="!loading" :size="16" />
          </el-button>
        </el-tooltip>
        <el-button type="primary" @click="publishOpen = true"><Upload :size="16" />发布新版本</el-button>
      </div>
    </div>

    <section v-if="latest" class="current-release-band">
      <span class="release-symbol"><Laptop :size="20" /></span>
      <div><strong>WinApp {{ latest.version }}</strong><span>{{ latest.notes || "未填写更新日志" }}</span></div>
      <div class="release-band-meta"><span>{{ fileSize(latest.size) }}</span><span>{{ formatDate(latest.published_at) }}</span></div>
    </section>

    <div v-loading="loading" class="table-frame">
      <el-table :data="releases" row-key="version">
        <el-table-column label="版本" width="130"><template #default="{ row }"><code>{{ row.version }}</code></template></el-table-column>
        <el-table-column label="更新日志" min-width="280"><template #default="{ row }"><span class="release-notes">{{ row.notes || "-" }}</span></template></el-table-column>
        <el-table-column label="大小" width="120"><template #default="{ row }">{{ fileSize(row.size) }}</template></el-table-column>
        <el-table-column label="发布人" width="150"><template #default="{ row }">{{ row.published_by?.display_name || row.published_by?.username || "-" }}</template></el-table-column>
        <el-table-column label="发布时间" width="180"><template #default="{ row }">{{ formatDate(row.published_at) }}</template></el-table-column>
        <template #empty><div class="table-empty"><Laptop :size="24" /><span>尚未发布 WinApp 版本</span></div></template>
      </el-table>
    </div>
  </div>

  <el-dialog v-model="publishOpen" title="发布 WinApp 新版本" width="min(560px, 94vw)" @closed="resetPublish">
    <el-form label-position="top">
      <el-form-item label="发布 ZIP" required>
        <el-upload :auto-upload="false" :limit="1" accept=".zip" :on-change="selectBundle" :on-remove="clearBundle">
          <el-button><Upload :size="15" />选择构建产物</el-button>
        </el-upload>
      </el-form-item>
      <el-form-item label="更新日志">
        <el-input v-model="notes" type="textarea" :rows="6" maxlength="20000" show-word-limit />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="publishOpen = false">取消</el-button>
      <el-button type="primary" :loading="publishing" @click="publishRelease">发布更新</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.current-release-band { display: flex; align-items: center; gap: 12px; padding: 14px 16px; color: #205c43; background: #f3faf6; border: 1px solid #cfe3d7; border-radius: 6px; }
.release-symbol { display: grid; place-items: center; width: 40px; height: 40px; color: #fff; background: #297052; border-radius: 6px; flex: 0 0 auto; }
.current-release-band strong, .current-release-band span { display: block; }
.current-release-band > div > span { margin-top: 3px; color: #607168; font-size: 12px; white-space: pre-wrap; }
.release-band-meta { display: flex; gap: 16px; margin-left: auto; color: #607168; font-size: 12px; text-align: right; }
.release-notes { display: block; max-width: 100%; overflow: hidden; white-space: pre-wrap; word-break: break-word; }
:global(html.dark) .current-release-band { color: #bce6d0; background: #1f2d26; border-color: #355444; }
@media (max-width: 700px) {
  .current-release-band { align-items: flex-start; flex-wrap: wrap; }
  .release-band-meta { width: 100%; margin-left: 52px; text-align: left; }
}
</style>
