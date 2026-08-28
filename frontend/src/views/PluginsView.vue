<script setup>
import { computed, onMounted, ref } from "vue";
import { ArchiveRestore, Boxes, FileCode2, Package, Plus, RefreshCw, Save, Search, Trash2, Upload } from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { request } from "../api";
import { hasPermission } from "../session";
import TargetSelectionTree from "../components/TargetSelectionTree.vue";
import CodeEditor from "../components/files/CodeEditor.vue";

const loading = ref(false);
const submitting = ref(false);
const catalog = ref([]);
const nodeContents = ref([]);
const search = ref("");
const typeFilter = ref("");
const uploadOpen = ref(false);
const deployOpen = ref(false);
const configOpen = ref(false);
const uploadJAR = ref(null);
const uploadConfig = ref(null);
const uploadForm = ref({ pluginType: "spigot", autoInstall: false, configDirectory: "" });
const deployForm = ref({ pluginType: "spigot", pluginId: "", artifactId: null, rules: [] });
const configForm = ref({ pluginType: "spigot", pluginId: "", artifactId: null, rules: [] });
const configFiles = ref([]);
const configPath = ref("");
const configContent = ref("");
const savedConfigContent = ref("");
const configLoading = ref(false);

const canUpload = computed(() => hasPermission("plugin.upload"));
const canDeploy = computed(() => hasPermission("plugin.deploy"));
const canRemove = computed(() => hasPermission("plugin.remove"));
const configDirty = computed(() => configPath.value && configContent.value !== savedConfigContent.value);
const filteredCatalog = computed(() => {
  const keyword = search.value.trim().toLowerCase();
  return catalog.value.filter((item) => {
    const artifact = currentArtifact(item);
    return (!typeFilter.value || item.plugin_type === typeFilter.value)
      && (!keyword || [item.name, item.plugin_id, artifact?.version, artifact?.main]
        .join(" ").toLowerCase().includes(keyword));
  });
});
const deployPlugin = computed(() => catalog.value.find((item) => (
  item.plugin_id === deployForm.value.pluginId && item.plugin_type === deployForm.value.pluginType
)));
const configPlugin = computed(() => catalog.value.find((item) => (
  item.plugin_id === configForm.value.pluginId && item.plugin_type === configForm.value.pluginType
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
  uploadForm.value = { pluginType: "spigot", autoInstall: false, configDirectory: "" };
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
    body.append("plugin_type", uploadForm.value.pluginType);
    body.append("auto_install", String(uploadForm.value.autoInstall));
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

async function removePlugin(plugin) {
  try {
    await ElMessageBox.confirm(
      `将删除“${plugin.name}”及其全部仓库制品和配置快照，已部署到服务器的插件不会受影响。此操作不可恢复。`,
      "删除插件",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" },
    );
    submitting.value = true;
    await request(`/api/v1/plugins/${encodeURIComponent(plugin.plugin_type)}/${encodeURIComponent(plugin.plugin_id)}`, {
      method: "DELETE",
    });
    ElMessage.success("插件已从仓库删除");
    await load(true);
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message || "插件删除失败");
  } finally {
    submitting.value = false;
  }
}

async function openDeploy(plugin, artifact = currentArtifact(plugin)) {
  deployForm.value = {
    pluginType: plugin.plugin_type,
    pluginId: plugin.plugin_id,
    artifactId: artifact?.artifact_id || null,
    rules: [],
  };
  deployOpen.value = true;
  try {
    const query = "?plugin_type=" + encodeURIComponent(plugin.plugin_type)
      + "&plugin_id=" + encodeURIComponent(plugin.plugin_id);
    const data = await request("/api/v1/plugin-deploy-preferences" + query);
    deployForm.value.rules = data.rules || [];
  } catch (error) {
    ElMessage.error(error.message);
  }
}

async function openConfig(plugin, artifact) {
  configForm.value = {
    pluginType: plugin.plugin_type,
    pluginId: plugin.plugin_id,
    artifactId: artifact.artifact_id,
    rules: [],
  };
  configFiles.value = [];
  configPath.value = "";
  configContent.value = "";
  savedConfigContent.value = "";
  configOpen.value = true;
  configLoading.value = true;
  try {
    const base = pluginArtifactPath(configForm.value);
    const query = "?plugin_type=" + encodeURIComponent(plugin.plugin_type)
      + "&plugin_id=" + encodeURIComponent(plugin.plugin_id);
    const [config, preferences] = await Promise.all([
      request(base + "/config"),
      canDeploy.value ? request("/api/v1/plugin-deploy-preferences" + query) : Promise.resolve({ rules: [] }),
    ]);
    configFiles.value = config.files || [];
    configForm.value.rules = preferences.rules || [];
    if (configFiles.value.length) await selectConfigFile(configFiles.value[0].path);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    configLoading.value = false;
  }
}

function pluginArtifactPath(form) {
  return "/api/v1/plugins/" + encodeURIComponent(form.pluginType) + "/"
    + encodeURIComponent(form.pluginId) + "/" + encodeURIComponent(form.artifactId);
}

async function selectConfigFile(path) {
  if (path === configPath.value) return;
  if (configDirty.value) {
    ElMessage.warning("请先保存当前配置文件");
    return;
  }
  configLoading.value = true;
  try {
    const data = await request(pluginArtifactPath(configForm.value) + "/config?path=" + encodeURIComponent(path));
    configPath.value = data.path;
    configContent.value = data.content;
    savedConfigContent.value = data.content;
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    configLoading.value = false;
  }
}

async function saveConfig() {
  if (!configPath.value || !configDirty.value) return;
  submitting.value = true;
  try {
    await request(pluginArtifactPath(configForm.value) + "/config?path=" + encodeURIComponent(configPath.value), {
      method: "PUT", body: JSON.stringify({ content: configContent.value }),
    });
    savedConfigContent.value = configContent.value;
    ElMessage.success("配置已保存");
    await load(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

async function deployConfig() {
  if (configDirty.value) {
    ElMessage.warning("请先保存配置文件");
    return;
  }
  submitting.value = true;
  try {
    const result = await request(pluginArtifactPath(configForm.value) + "/config/deploy", {
      method: "POST", body: JSON.stringify({ rules: configForm.value.rules }),
    });
    const failed = (result.targets || []).filter((item) => item.error);
    if (failed.length) ElMessage.warning("配置部署完成，其中 " + failed.length + " 个目标失败");
    else ElMessage.success("配置已部署到 " + (result.targets?.length || 0) + " 个服务器");
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

async function deploy() {
  if (!deployForm.value.artifactId) {
    ElMessage.warning("请选择制品版本");
    return;
  }
  submitting.value = true;
  try {
    const path = pluginArtifactPath(deployForm.value) + "/deploy";
    const result = await request(path, {
      method: "POST", body: JSON.stringify({ rules: deployForm.value.rules }),
    });
    const failed = (result.targets || []).filter((item) => item.error);
    if (failed.length) ElMessage.warning("部署完成，其中 " + failed.length + " 个目标失败");
    else ElMessage.success("插件 JAR 已部署到 " + (result.targets?.length || 0) + " 个服务器");
    deployOpen.value = false;
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
      <el-select v-model="typeFilter" class="status-filter" clearable placeholder="全部平台">
        <el-option label="Spigot / Paper" value="spigot" />
        <el-option label="Velocity" value="velocity" />
        <el-option label="BungeeCord" value="bungee" />
      </el-select>
    </div>

    <div v-loading="loading" class="table-frame plugin-repository-table">
      <el-table :data="filteredCatalog" :row-key="(row) => row.plugin_type + ':' + row.plugin_id">
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
                <el-button v-if="artifact.config?.present && (canUpload || canDeploy)" size="small" plain @click="openConfig(row, artifact)">
                  <FileCode2 :size="14" />配置
                </el-button>
                <el-button v-if="canDeploy" size="small" plain @click="openDeploy(row, artifact)">
                  <Upload :size="14" />部署插件
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
        <el-table-column label="平台" width="130">
          <template #default="{ row }">
            <el-tag effect="plain">{{ row.plugin_type === "spigot" ? "Spigot / Paper" : row.plugin_type }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="新服安装" width="110">
          <template #default="{ row }">
            <el-tag :type="row.auto_install ? 'success' : 'info'" effect="plain">
              {{ row.auto_install ? "自动" : "手动" }}
            </el-tag>
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
        <el-table-column label="操作" width="150" align="right">
          <template #default="{ row }">
            <el-button v-if="canDeploy" type="primary" link @click="openDeploy(row)"><Upload :size="14" />部署</el-button>
            <el-button v-if="canRemove" type="danger" link :disabled="submitting" @click="removePlugin(row)"><Trash2 :size="14" />删除</el-button>
          </template>
        </el-table-column>
        <template #empty><div class="table-empty"><Package :size="24" /><span>仓库中暂无插件</span></div></template>
      </el-table>
    </div>
  </div>

  <el-dialog v-model="uploadOpen" title="上传插件制品" width="min(560px, 94vw)" @closed="resetUpload">
    <el-form label-position="top">
      <div class="dialog-form-grid">
        <el-form-item label="插件平台" required>
          <el-select v-model="uploadForm.pluginType" class="full-control">
            <el-option label="Spigot / Paper" value="spigot" />
            <el-option label="Velocity" value="velocity" />
            <el-option label="BungeeCord" value="bungee" />
          </el-select>
        </el-form-item>
        <el-form-item label="新服务器自动安装">
          <el-switch v-model="uploadForm.autoInstall" />
        </el-form-item>
      </div>
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

  <el-dialog v-model="deployOpen" title="部署插件" width="min(720px, 94vw)">
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
      <el-form-item label="部署目标">
        <TargetSelectionTree
          v-model="deployForm.rules"
          :nodes="nodeContents"
          :plugin-type="deployForm.pluginType"
          :disabled="submitting"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="deployOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="deploy"><Upload :size="15" />部署</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="configOpen" title="编辑插件配置" width="min(1080px, 96vw)" destroy-on-close>
    <div v-loading="configLoading" class="plugin-config-editor">
      <aside class="plugin-config-files">
        <strong>{{ configPlugin?.name }} {{ configForm.artifactId ? '#' + configForm.artifactId : '' }}</strong>
        <button
          v-for="file in configFiles"
          :key="file.path"
          type="button"
          :class="{ active: file.path === configPath }"
          :disabled="submitting"
          @click="selectConfigFile(file.path)"
        >{{ file.path }}</button>
        <span v-if="!configFiles.length">该制品没有可编辑的配置文件。</span>
      </aside>
      <section class="plugin-config-content">
        <div class="plugin-config-heading"><strong>{{ configPath || '选择配置文件' }}</strong></div>
        <CodeEditor
          v-if="configPath"
          v-model="configContent"
          :disabled="!canUpload || submitting"
          :file-path="configPath"
          @save="saveConfig"
        />
      </section>
    </div>
    <el-form v-if="canDeploy" class="plugin-config-targets" label-position="top">
      <el-form-item label="配置部署目标">
        <TargetSelectionTree
          v-model="configForm.rules"
          :nodes="nodeContents"
          :plugin-type="configForm.pluginType"
          :disabled="submitting || configDirty"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="configOpen = false">关闭</el-button>
      <el-button v-if="canUpload" type="primary" :disabled="!configDirty" :loading="submitting" @click="saveConfig"><Save :size="15" />保存配置</el-button>
      <el-button v-if="canDeploy" type="primary" :disabled="configDirty || !configFiles.length" :loading="submitting" @click="deployConfig"><Upload :size="15" />部署配置</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.plugin-config-editor { display: grid; min-height: 480px; grid-template-columns: minmax(190px, 260px) minmax(0, 1fr); border: 1px solid var(--app-border); }
.plugin-config-files { display: flex; min-width: 0; flex-direction: column; gap: 3px; border-right: 1px solid var(--app-border); padding: 10px; background: var(--app-surface-muted); }
.plugin-config-files strong { overflow: hidden; padding: 4px 6px 8px; color: var(--app-text); font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.plugin-config-files button { overflow: hidden; border: 1px solid transparent; border-radius: 4px; padding: 7px 8px; color: var(--app-text-secondary); background: transparent; cursor: pointer; font: 12px/1.3 Consolas, monospace; text-align: left; text-overflow: ellipsis; white-space: nowrap; }
.plugin-config-files button:hover, .plugin-config-files button.active { border-color: var(--app-border); color: var(--app-text); background: var(--app-surface-hover); }
.plugin-config-files button:disabled { cursor: not-allowed; opacity: 0.55; }
.plugin-config-files span { padding: 8px 6px; color: var(--app-text-muted); font-size: 12px; }
.plugin-config-content { display: flex; min-width: 0; flex-direction: column; }
.plugin-config-heading { min-height: 40px; border-bottom: 1px solid var(--app-border); padding: 11px 13px; color: var(--app-text); font-size: 12px; }
.plugin-config-content :deep(.code-editor), .plugin-config-content :deep(.cm-editor), .plugin-config-content :deep(.cm-scroller) { min-height: 438px; height: 100%; }
.plugin-config-targets { margin-top: 16px; }
@media (max-width: 720px) { .plugin-config-editor { min-height: 580px; grid-template-columns: 1fr; grid-template-rows: minmax(108px, auto) minmax(0, 1fr); } .plugin-config-files { max-height: 150px; border-right: 0; border-bottom: 1px solid var(--app-border); overflow-y: auto; } .plugin-config-content :deep(.code-editor), .plugin-config-content :deep(.cm-editor), .plugin-config-content :deep(.cm-scroller) { min-height: 390px; } }
</style>
