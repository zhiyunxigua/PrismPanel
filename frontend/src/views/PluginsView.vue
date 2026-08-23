<script setup>
import { computed, onMounted, ref } from "vue";
import { ArchiveRestore, Boxes, FileCode2, Info, Package, Plus, RefreshCw, Save, Search, Upload } from "lucide-vue-next";
import { ElMessage } from "element-plus";
import { request } from "../api";
import { apiURL, runtimeConfig, runtimeHeaders } from "../runtime";
import { hasPermission } from "../session";
import TargetSelectionTree from "../components/TargetSelectionTree.vue";
import CodeEditor from "../components/files/CodeEditor.vue";

const loading = ref(false);
const submitting = ref(false);
const catalog = ref([]);
const nodeContents = ref([]);
const search = ref("");
const typeFilter = ref("");
const viewMode = ref("plugins");
const uploadOpen = ref(false);
const deployOpen = ref(false);
const configOpen = ref(false);
const detailOpen = ref(false);
const detailForm = ref(null);
const icons = ref({});
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
const configDirty = computed(() => configPath.value && configContent.value !== savedConfigContent.value);
const filteredCatalog = computed(() => {
  const keyword = search.value.trim().toLowerCase();
  return catalog.value.filter((item) => {
    if (viewMode.value === "mods" && !["fabric", "forge"].includes(item.plugin_type)) return false;
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
// modByID: 仓库内 mod 的 fabric id（小写）→ 仓库条目，用于依赖"已收录"标记与跳转。
const modByID = computed(() => {
  const map = new Map();
  for (const item of catalog.value) {
    if (!["fabric", "forge"].includes(item.plugin_type)) continue;
    const id = modID(item);
    if (id) map.set(String(id).toLowerCase(), item);
  }
  return map;
});

function currentArtifact(plugin) {
  return (plugin?.artifacts || []).find((item) => item.artifact_id === plugin.current_artifact_id)
    || plugin?.artifacts?.[0];
}

function modMeta(artifact) {
  return artifact?.mod_metadata || artifact?.descriptors?.fabric?.mod_metadata || null;
}

// modID 取仓库条目的 mod id：优先持久化元数据（fabric），其次遍历描述符
// 任意 key（fabric/forge 的 descriptor map key 分别为 "fabric"/"forge"，
// panel 端 parseForgeModsTOML 已填充 ID），最后回退描述符名称。
function modID(plugin) {
  const artifact = currentArtifact(plugin);
  const meta = modMeta(artifact);
  if (meta?.id) return meta.id;
  const descriptors = artifact?.descriptors || {};
  const entries = Object.values(descriptors).filter(Boolean);
  for (const descriptor of entries) {
    if (descriptor.id) return descriptor.id;
  }
  for (const descriptor of entries) {
    if (descriptor.name) return descriptor.name;
  }
  return "";
}

function modIcon(plugin, artifact = currentArtifact(plugin)) {
  if (!plugin || !artifact) return "";
  const key = [plugin.plugin_type, plugin.plugin_id, artifact.artifact_id].join(":");
  if (icons.value[key] !== undefined) return icons.value[key] || "";
  loadModIcon(key, plugin, artifact);
  return "";
}

function loadModIcon(key, plugin, artifact) {
  icons.value[key] = null;
  const url = apiURL("/api/v1/plugins/" + encodeURIComponent(plugin.plugin_type) + "/"
    + encodeURIComponent(plugin.plugin_id) + "/" + encodeURIComponent(artifact.artifact_id) + "/icon");
  fetch(url, {
    headers: runtimeHeaders(),
    credentials: runtimeConfig.proxySession ? "omit" : "same-origin",
  })
    .then((response) => {
      if (!response.ok) throw new Error(String(response.status));
      return response.blob();
    })
    .then((blob) => { icons.value[key] = URL.createObjectURL(blob); })
    .catch(() => { icons.value[key] = ""; });
}

function environmentLabel(value) {
  switch (value) {
    case "client": return "客户端";
    case "server": return "服务端";
    case "*": return "双端";
    default: return value || "未知";
  }
}

function environmentTagType(value) {
  switch (value) {
    case "client": return "warning";
    case "server": return "success";
    case "*": return "info";
    default: return "info";
  }
}

// collectedDep: 依赖 id 在仓库内已有对应 mod 时返回其条目，否则 null。
function collectedDep(dep) {
  if (!dep?.id) return null;
  return modByID.value.get(String(dep.id).toLowerCase()) || null;
}

function dependencyCounts(meta) {
  return {
    depends: (meta?.depends || []).length,
    suggests: (meta?.suggests || []).length,
    collected: (meta?.depends || []).filter((dep) => collectedDep(dep)).length,
  };
}

function openDetail(plugin, artifact = currentArtifact(plugin)) {
  detailForm.value = { plugin, artifact };
  detailOpen.value = true;
  modIcon(plugin, artifact);
}

// 从详情里的依赖跳转到仓库内已收录 mod。
function jumpToMod(item) {
  if (!item) return;
  detailOpen.value = false;
  if (viewMode.value !== "mods") viewMode.value = "mods";
  openDetail(item, currentArtifact(item));
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
  uploadForm.value = {
    pluginType: viewMode.value === "mods" ? "fabric" : "spigot",
    autoInstall: false,
    configDirectory: "",
  };
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

function platformLabel(type) {
  switch (type) {
    case "spigot": return "Spigot / Paper";
    case "velocity": return "Velocity";
    case "bungee": return "BungeeCord";
    case "fabric": return "Fabric 模组";
    case "forge": return "Forge 模组";
    default: return type;
  }
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
          <Plus :size="16" />{{ viewMode === "mods" ? "上传 Mod" : "上传插件" }}
        </el-button>
      </div>
    </div>

    <el-tabs v-model="viewMode" class="repo-tabs">
      <el-tab-pane label="插件仓库" name="plugins" />
      <el-tab-pane label="Mod 仓库" name="mods" />
    </el-tabs>

    <div class="table-toolbar">
      <el-input
        v-model="search"
        class="search-input"
        clearable
        :placeholder="viewMode === 'mods' ? '搜索模组名称、ID 或版本' : '搜索名称、版本或主类'"
      >
        <template #prefix><Search :size="15" /></template>
      </el-input>
      <el-select v-model="typeFilter" class="status-filter" clearable :placeholder="viewMode === 'mods' ? '全部 Mod 平台' : '全部平台'">
        <template v-if="viewMode === 'mods'">
          <el-option label="Fabric 模组" value="fabric" />
          <el-option label="Forge 模组" value="forge" />
        </template>
        <template v-else>
          <el-option label="Spigot / Paper" value="spigot" />
          <el-option label="Velocity" value="velocity" />
          <el-option label="BungeeCord" value="bungee" />
          <el-option label="Fabric 模组" value="fabric" />
          <el-option label="Forge 模组" value="forge" />
        </template>
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
                  <Upload :size="14" />部署{{ viewMode === "mods" ? " Mod" : "" }}
                </el-button>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column v-if="viewMode === 'plugins'" label="插件" min-width="230">
          <template #default="{ row }">
            <div class="node-cell">
              <span class="node-symbol"><Package :size="16" /></span>
              <div><strong>{{ row.name }}</strong><small>{{ currentArtifact(row)?.main || row.plugin_id }}</small></div>
            </div>
          </template>
        </el-table-column>
        <el-table-column v-if="viewMode === 'mods'" label="模组" min-width="240">
          <template #default="{ row }">
            <div class="node-cell">
              <span class="mod-icon-wrap">
                <img v-if="modIcon(row)" :src="modIcon(row)" class="mod-icon" alt="" />
                <Package v-else :size="18" />
              </span>
              <div>
                <strong>{{ row.name }}</strong>
                <small><code>{{ modID(row) }}</code> · {{ currentArtifact(row)?.version || "-" }}</small>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="平台" width="130">
          <template #default="{ row }">
            <el-tag effect="plain">{{ platformLabel(row.plugin_type) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="viewMode === 'mods'" label="环境" width="90">
          <template #default="{ row }">
            <el-tag :type="environmentTagType(modMeta(currentArtifact(row))?.environment)" effect="plain">
              {{ environmentLabel(modMeta(currentArtifact(row))?.environment) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="viewMode === 'plugins'" label="新服安装" width="110">
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
        <el-table-column v-if="viewMode === 'mods'" label="依赖" width="120">
          <template #default="{ row }">
            <span v-if="dependencyCounts(modMeta(currentArtifact(row))).depends" class="mod-dep-count">
              必装 {{ dependencyCounts(modMeta(currentArtifact(row))).depends }}
              <small v-if="dependencyCounts(modMeta(currentArtifact(row))).suggests">+ 建议 {{ dependencyCounts(modMeta(currentArtifact(row))).suggests }}</small>
            </span>
            <span v-else class="mod-dep-count muted">无必装依赖</span>
          </template>
        </el-table-column>
        <el-table-column v-if="viewMode === 'mods'" label="已收录" width="100">
          <template #default="{ row }">
            <template v-if="dependencyCounts(modMeta(currentArtifact(row))).depends">
              <el-tag :type="dependencyCounts(modMeta(currentArtifact(row))).collected === dependencyCounts(modMeta(currentArtifact(row))).depends ? 'success' : 'warning'" effect="plain">
                {{ dependencyCounts(modMeta(currentArtifact(row))).collected }}/{{ dependencyCounts(modMeta(currentArtifact(row))).depends }}
              </el-tag>
            </template>
            <span v-else class="table-muted">-</span>
          </template>
        </el-table-column>
        <el-table-column v-if="viewMode === 'plugins'" label="配置" width="130">
          <template #default="{ row }">
            <el-tag :type="currentArtifact(row)?.config?.present ? 'success' : 'info'" effect="plain">
              {{ currentArtifact(row)?.config?.present ? currentArtifact(row).config.files + " 个文件" : "无快照" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="制品" width="90"><template #default="{ row }">{{ row.artifacts?.length || 0 }}</template></el-table-column>
        <el-table-column label="操作" :width="viewMode === 'mods' ? 170 : 110" align="right">
          <template #default="{ row }">
            <el-button v-if="viewMode === 'mods'" type="primary" link @click="openDetail(row)"><Info :size="14" />详情</el-button>
            <el-button v-if="canDeploy" type="primary" link @click="openDeploy(row)"><Upload :size="14" />部署</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <div class="table-empty">
            <Package :size="24" />
            <span>{{ viewMode === "mods" ? "仓库中暂无 Mod" : "仓库中暂无插件" }}</span>
          </div>
        </template>
      </el-table>
    </div>
  </div>

  <el-dialog v-model="uploadOpen" :title="viewMode === 'mods' ? '上传 Mod 制品' : '上传插件制品'" width="min(560px, 94vw)" @closed="resetUpload">
    <el-form label-position="top">
      <div class="dialog-form-grid">
        <el-form-item :label="viewMode === 'mods' ? 'Mod 平台' : '插件平台'" required>
          <el-select v-model="uploadForm.pluginType" class="full-control">
            <template v-if="viewMode === 'mods'">
              <el-option label="Fabric 模组" value="fabric" />
              <el-option label="Forge 模组" value="forge" />
            </template>
            <template v-else>
              <el-option label="Spigot / Paper" value="spigot" />
              <el-option label="Velocity" value="velocity" />
              <el-option label="BungeeCord" value="bungee" />
              <el-option label="Fabric 模组" value="fabric" />
              <el-option label="Forge 模组" value="forge" />
            </template>
          </el-select>
        </el-form-item>
        <el-form-item label="新服务器自动安装">
          <el-switch v-model="uploadForm.autoInstall" />
        </el-form-item>
      </div>
      <el-form-item :label="viewMode === 'mods' ? 'Mod JAR' : '插件 JAR'" required>
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

  <el-dialog v-model="detailOpen" title="Mod 详情" width="min(760px, 94vw)">
    <div v-if="detailForm" class="mod-detail">
      <div class="mod-detail-head">
        <span class="mod-icon-wrap detail">
          <img v-if="modIcon(detailForm.plugin, detailForm.artifact)" :src="modIcon(detailForm.plugin, detailForm.artifact)" class="mod-icon" alt="" />
          <Package v-else :size="28" />
        </span>
        <div class="mod-detail-title">
          <div class="mod-detail-name">
            <strong>{{ detailForm.plugin.name }}</strong>
            <code>{{ modMeta(detailForm.artifact)?.id || modID(detailForm.plugin) }}</code>
          </div>
          <div class="mod-detail-tags">
            <el-tag effect="plain">{{ platformLabel(detailForm.plugin.plugin_type) }}</el-tag>
            <el-tag :type="environmentTagType(modMeta(detailForm.artifact)?.environment)" effect="plain">
              {{ environmentLabel(modMeta(detailForm.artifact)?.environment) }}
            </el-tag>
            <el-tag type="primary" effect="plain"><code>{{ detailForm.artifact.version }}</code></el-tag>
            <el-tag v-if="modMeta(detailForm.artifact)?.license" effect="plain">{{ modMeta(detailForm.artifact).license }}</el-tag>
            <el-tag v-if="modMeta(detailForm.artifact)?.schema_version" effect="plain">schema v{{ modMeta(detailForm.artifact).schema_version }}</el-tag>
          </div>
        </div>
      </div>

      <p v-if="detailForm.artifact.description" class="mod-detail-description">{{ detailForm.artifact.description }}</p>
      <p v-else class="mod-detail-description muted">该 Mod 未提供描述。</p>

      <div class="mod-detail-section">
        <h4>必装依赖（depends）</h4>
        <div v-if="(modMeta(detailForm.artifact)?.depends || []).length" class="mod-dep-list">
          <div v-for="dep in modMeta(detailForm.artifact).depends" :key="dep.id" class="mod-dep-item">
            <template v-if="collectedDep(dep)">
              <el-button link type="primary" @click="jumpToMod(collectedDep(dep))">
                <span class="dep-id">{{ dep.id }}</span>
                <span class="dep-range">{{ dep.version_range || "*" }}</span>
              </el-button>
              <el-tag type="success" effect="plain" size="small">已收录</el-tag>
            </template>
            <template v-else>
              <span class="dep-id">{{ dep.id }}</span>
              <span class="dep-range">{{ dep.version_range || "*" }}</span>
              <el-tag type="info" effect="plain" size="small">未收录</el-tag>
            </template>
          </div>
        </div>
        <div v-else class="mod-detail-empty">无必装依赖</div>
      </div>

      <div class="mod-detail-section">
        <h4>建议依赖（suggests）</h4>
        <div v-if="(modMeta(detailForm.artifact)?.suggests || []).length" class="mod-dep-list">
          <div v-for="dep in modMeta(detailForm.artifact).suggests" :key="dep.id" class="mod-dep-item">
            <template v-if="collectedDep(dep)">
              <el-button link type="primary" @click="jumpToMod(collectedDep(dep))">
                <span class="dep-id">{{ dep.id }}</span>
                <span class="dep-range">{{ dep.version_range || "*" }}</span>
              </el-button>
              <el-tag type="success" effect="plain" size="small">已收录</el-tag>
            </template>
            <template v-else>
              <span class="dep-id">{{ dep.id }}</span>
              <span class="dep-range">{{ dep.version_range || "*" }}</span>
              <el-tag type="info" effect="plain" size="small">未收录</el-tag>
            </template>
          </div>
        </div>
        <div v-else class="mod-detail-empty">无建议依赖</div>
      </div>

      <div v-if="(modMeta(detailForm.artifact)?.entrypoints || []).length" class="mod-detail-section">
        <h4>入口点（entrypoints）</h4>
        <div class="mod-entrypoint-list">
          <div v-for="entry in modMeta(detailForm.artifact).entrypoints" :key="entry.kind" class="mod-entrypoint-item">
            <el-tag effect="plain" size="small">{{ entry.kind }}</el-tag>
            <code>{{ entry.values?.join(", ") }}</code>
          </div>
        </div>
      </div>

      <div class="mod-detail-meta">
        <span v-if="modMeta(detailForm.artifact)?.icon">图标：<code>{{ modMeta(detailForm.artifact).icon }}</code></span>
        <span v-if="detailForm.artifact.website"><a :href="detailForm.artifact.website" target="_blank" rel="noreferrer">{{ detailForm.artifact.website }}</a></span>
        <span v-if="detailForm.artifact.artifact?.original_filename"><code>{{ detailForm.artifact.artifact.original_filename }}</code></span>
      </div>
    </div>
    <template #footer>
      <el-button @click="detailOpen = false">关闭</el-button>
      <el-button v-if="canDeploy" type="primary" @click="detailOpen = false; openDeploy(detailForm.plugin, detailForm.artifact)"><Upload :size="15" />部署此版本</el-button>
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
.repo-tabs { margin-bottom: 12px; }
.mod-icon-wrap { display: inline-flex; width: 34px; height: 34px; flex: none; align-items: center; justify-content: center; border: 1px solid var(--app-border); border-radius: 6px; overflow: hidden; color: var(--app-text-muted); background: var(--app-surface-muted); }
.mod-icon-wrap.detail { width: 52px; height: 52px; border-radius: 8px; }
.mod-icon { width: 100%; height: 100%; object-fit: contain; }
.mod-dep-count { font-size: 12px; color: var(--app-text); }
.mod-dep-count.muted, .table-muted { color: var(--app-text-muted); }
.mod-detail { display: flex; flex-direction: column; gap: 14px; }
.mod-detail-head { display: flex; align-items: center; gap: 14px; }
.mod-detail-title { display: flex; min-width: 0; flex-direction: column; gap: 6px; }
.mod-detail-name { display: flex; align-items: center; gap: 10px; }
.mod-detail-name code { color: var(--app-text-secondary); }
.mod-detail-tags { display: flex; flex-wrap: wrap; gap: 6px; }
.mod-detail-description { margin: 0; color: var(--app-text); font-size: 13px; line-height: 1.6; }
.mod-detail-description.muted { color: var(--app-text-muted); }
.mod-detail-section h4 { margin: 0 0 8px; font-size: 12px; color: var(--app-text-secondary); }
.mod-dep-list { display: flex; flex-direction: column; gap: 6px; }
.mod-dep-item { display: flex; align-items: center; gap: 8px; min-height: 28px; }
.mod-dep-item .el-button { min-height: 26px; padding: 0 4px; }
.dep-id { font: 12px/1.4 Consolas, monospace; color: var(--app-text); }
.dep-range { font: 12px/1.4 Consolas, monospace; color: var(--app-text-muted); }
.mod-detail-empty { color: var(--app-text-muted); font-size: 12px; }
.mod-entrypoint-list { display: flex; flex-direction: column; gap: 6px; }
.mod-entrypoint-item { display: flex; align-items: center; gap: 10px; min-height: 26px; }
.mod-entrypoint-item code { font-size: 12px; color: var(--app-text-secondary); }
.mod-detail-meta { display: flex; flex-wrap: wrap; gap: 6px 18px; padding-top: 10px; border-top: 1px solid var(--app-border); color: var(--app-text-muted); font-size: 12px; }
.mod-detail-meta a { color: var(--app-accent); }
@media (max-width: 720px) { .plugin-config-editor { min-height: 580px; grid-template-columns: 1fr; grid-template-rows: minmax(108px, auto) minmax(0, 1fr); } .plugin-config-files { max-height: 150px; border-right: 0; border-bottom: 1px solid var(--app-border); overflow-y: auto; } .plugin-config-content :deep(.code-editor), .plugin-config-content :deep(.cm-editor), .plugin-config-content :deep(.cm-scroller) { min-height: 390px; } }
</style>
