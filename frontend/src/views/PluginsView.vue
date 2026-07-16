<script setup>
import { computed, onMounted, ref } from "vue";
import { ArchiveRestore, Boxes, Package, Plus, RefreshCw, Search, Upload } from "lucide-vue-next";
import { ElMessage } from "element-plus";
import { request } from "../api";
import { hasPermission } from "../session";

const loading = ref(false);
const submitting = ref(false);
const catalog = ref([]);
const nodeContents = ref([]);
const search = ref("");
const uploadOpen = ref(false);
const deployOpen = ref(false);
const uploadJAR = ref(null);
const uploadConfig = ref(null);
const uploadForm = ref({ configDirectory: "" });
const deployForm = ref({ pluginId: "", artifactId: null, target: "" });

const canUpload = computed(() => hasPermission("plugin.upload"));
const canDeploy = computed(() => hasPermission("plugin.deploy"));
const filteredCatalog = computed(() => {
  const keyword = search.value.trim().toLowerCase();
  if (!keyword) return catalog.value;
  return catalog.value.filter((item) => {
    const artifact = currentArtifact(item);
    return [item.name, item.plugin_id, artifact?.version, artifact?.main]
      .join(" ").toLowerCase().includes(keyword);
  });
});
const deployPlugin = computed(() => catalog.value.find((item) => item.plugin_id === deployForm.value.pluginId));
const deployTargets = computed(() => nodeContents.value.flatMap((content) => (
  (content.servers || []).map((item) => ({
    value: content.node.id + ":" + item.server_id,
    nodeId: content.node.id,
    serverId: item.server_id,
    label: item.name,
    nodeName: content.node.name,
    type: item.type,
  }))
)));

function currentArtifact(plugin) {
  return (plugin?.artifacts || []).find((item) => item.artifact_id === plugin.current_artifact_id)
    || plugin?.artifacts?.[0];
}

async function load(silent = false) {
  if (!silent) loading.value = true;
  try {
    const responses = await Promise.all([
      request("/api/v1/plugins"),
      canDeploy.value ? request("/api/v1/servers") : Promise.resolve({ nodes: [] }),
    ]);
    catalog.value = responses[0].items || [];
    nodeContents.value = responses[1].nodes || [];
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) loading.value = false;
  }
}

function handleJAR(file) { uploadJAR.value = file.raw || null; }
function handleConfig(file) { uploadConfig.value = file.raw || null; }
function clearJAR() { uploadJAR.value = null; }
function clearConfig() { uploadConfig.value = null; }

function resetUpload() {
  uploadJAR.value = null;
  uploadConfig.value = null;
  uploadForm.value = { configDirectory: "" };
}

async function uploadPlugin() {
  if (!uploadJAR.value) {
    ElMessage.warning("请选择插件 JAR");
    return;
  }
  submitting.value = true;
  try {
    const body = new FormData();
    body.append("jar", uploadJAR.value);
    if (uploadConfig.value) body.append("config", uploadConfig.value);
    if (uploadForm.value.configDirectory.trim()) {
      body.append("config_directory", uploadForm.value.configDirectory.trim());
    }
    const result = await request("/api/v1/plugins", { method: "POST", body });
    uploadOpen.value = false;
    resetUpload();
    ElMessage.success(result.duplicate ? "仓库中已有相同制品" : "插件制品已保存");
    await load(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

async function rescan() {
  submitting.value = true;
  try {
    const result = await request("/api/v1/plugins/rescan", { method: "POST", body: "{}" });
    catalog.value = result.plugins || [];
    const changed = (result.imported || 0) + (result.rebuilt_manifests || 0) + (result.recovered_changes || 0);
    ElMessage.success(changed ? "仓库扫描完成，处理 " + changed + " 项变更" : "仓库扫描完成");
    if (result.warnings?.length) ElMessage.warning(result.warnings.join("；"));
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

function openDeploy(plugin, artifact = currentArtifact(plugin)) {
  deployForm.value = {
    pluginId: plugin.plugin_id,
    artifactId: artifact?.artifact_id || null,
    target: deployTargets.value[0]?.value || "",
  };
  deployOpen.value = true;
}

async function deploy() {
  const target = deployTargets.value.find((item) => item.value === deployForm.value.target);
  if (!target || !deployForm.value.artifactId) {
    ElMessage.warning("请选择制品版本和服务器组");
    return;
  }
  submitting.value = true;
  try {
    const path = "/api/v1/plugins/" + encodeURIComponent(deployForm.value.pluginId) + "/" +
      encodeURIComponent(deployForm.value.artifactId) + "/deploy?node_id=" + encodeURIComponent(target.nodeId);
    const result = await request(path, {
      method: "POST", body: JSON.stringify({ server_id: target.serverId }),
    });
    deployOpen.value = false;
    ElMessage.success(result.pending_restart ? "部署完成，运行中的子服需要重启" : "插件已部署");
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
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
  return bytes >= 1024 * 1024
    ? (bytes / 1024 / 1024).toFixed(2) + " MiB"
    : (bytes / 1024).toFixed(1) + " KiB";
}

onMounted(load);
</script>

<template>
  <div class="content-stack">
    <div class="page-toolbar">
      <div><h2>插件仓库</h2><p>{{ catalog.length }} 个插件 · 本地文件为权威数据</p></div>
      <div class="toolbar-actions">
        <el-tooltip content="刷新">
          <el-button class="square-button" :loading="loading" aria-label="刷新" @click="load()">
            <RefreshCw v-if="!loading" :size="16" />
          </el-button>
        </el-tooltip>
        <el-button v-if="canUpload" :loading="submitting" @click="rescan">
          <ArchiveRestore :size="16" />重新扫描
        </el-button>
        <el-button v-if="canUpload" type="primary" @click="uploadOpen = true">
          <Plus :size="16" />上传插件
        </el-button>
      </div>
    </div>

    <div class="table-toolbar">
      <el-input v-model="search" class="search-input" clearable placeholder="搜索名称、版本或主类">
        <template #prefix><Search :size="15" /></template>
      </el-input>
    </div>

    <div v-loading="loading" class="table-frame plugin-repository-table">
      <el-table :data="filteredCatalog" row-key="plugin_id">
        <el-table-column type="expand">
          <template #default="{ row }">
            <div class="artifact-list">
              <div v-for="artifact in row.artifacts" :key="artifact.artifact_id" class="artifact-row">
                <div><strong>{{ artifact.version }}</strong><code>#{{ artifact.artifact_id }}</code></div>
                <span>{{ artifact.artifact.original_filename }}</span>
                <span>{{ fileSize(artifact.artifact.size) }}</span>
                <span>{{ formatDate(artifact.uploaded_at) }}</span>
                <span>{{ artifact.uploaded_by.display_name || artifact.uploaded_by.username || "本地导入" }}</span>
                <el-tag v-if="artifact.artifact_id === row.current_artifact_id" type="success" effect="plain">当前</el-tag>
                <el-button v-if="canDeploy" size="small" plain @click="openDeploy(row, artifact)">
                  <Upload :size="14" />部署
                </el-button>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="插件" min-width="230">
          <template #default="{ row }">
            <div class="node-cell">
              <span class="node-symbol"><Package :size="16" /></span>
              <div><strong>{{ row.name }}</strong><small>{{ currentArtifact(row)?.main || row.plugin_id }}</small></div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="当前版本" width="130">
          <template #default="{ row }"><code>{{ currentArtifact(row)?.version || "-" }}</code></template>
        </el-table-column>
        <el-table-column label="作者" min-width="160">
          <template #default="{ row }">{{ currentArtifact(row)?.authors?.join(", ") || "-" }}</template>
        </el-table-column>
        <el-table-column label="配置" width="130">
          <template #default="{ row }">
            <el-tag :type="currentArtifact(row)?.config?.present ? 'success' : 'info'" effect="plain">
              {{ currentArtifact(row)?.config?.present ? currentArtifact(row).config.files + " 个文件" : "无快照" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="制品" width="90"><template #default="{ row }">{{ row.artifacts?.length || 0 }}</template></el-table-column>
        <el-table-column label="操作" width="110" align="right">
          <template #default="{ row }">
            <el-button v-if="canDeploy" type="primary" link @click="openDeploy(row)"><Upload :size="14" />部署</el-button>
          </template>
        </el-table-column>
        <template #empty><div class="table-empty"><Package :size="24" /><span>仓库中暂无插件</span></div></template>
      </el-table>
    </div>
  </div>

  <el-dialog v-model="uploadOpen" title="上传插件制品" width="min(560px, 94vw)" @closed="resetUpload">
    <el-form label-position="top">
      <el-form-item label="插件 JAR" required>
        <el-upload :auto-upload="false" :limit="1" accept=".jar" :on-change="handleJAR" :on-remove="clearJAR">
          <el-button><Package :size="15" />选择 JAR</el-button>
        </el-upload>
      </el-form-item>
      <el-form-item label="配置快照 ZIP">
        <el-upload :auto-upload="false" :limit="1" accept=".zip" :on-change="handleConfig" :on-remove="clearConfig">
          <el-button><Boxes :size="15" />选择 ZIP</el-button>
        </el-upload>
      </el-form-item>
      <el-form-item label="配置目录名">
        <el-input v-model="uploadForm.configDirectory" maxlength="100" placeholder="默认使用插件 name" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="uploadOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="uploadPlugin">上传</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="deployOpen" title="部署插件" width="min(520px, 94vw)">
    <el-form label-position="top">
      <el-form-item label="插件"><el-input :model-value="deployPlugin?.name" disabled /></el-form-item>
      <el-form-item label="制品版本" required>
        <el-select v-model="deployForm.artifactId" class="full-control">
          <el-option
            v-for="artifact in deployPlugin?.artifacts || []"
            :key="artifact.artifact_id"
            :label="artifact.version + '  #' + artifact.artifact_id"
            :value="artifact.artifact_id"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="服务器组" required>
        <el-select v-model="deployForm.target" class="full-control" filterable>
          <el-option v-for="target in deployTargets" :key="target.value" :value="target.value" :label="target.label">
            <div class="deploy-target-option"><span>{{ target.label }}</span><small>{{ target.nodeName }} · {{ target.type === "mirror" ? "镜像组" : "固定实例" }}</small></div>
          </el-option>
        </el-select>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="deployOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="deploy"><Upload :size="15" />部署</el-button>
    </template>
  </el-dialog>
</template>
