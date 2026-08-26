<script setup>
import { computed, onMounted, ref, watch } from "vue";
import {
  AlertTriangle, ArchiveRestore, Boxes, CheckCircle2, FileCode2, FolderOpen, Info, Package,
  Plus, RefreshCw, Save, Search, ShieldAlert, Trash2, Upload,
} from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { request, requestWithProgress } from "../api";
import { apiURL, runtimeConfig, runtimeHeaders } from "../runtime";
import { hasPermission } from "../session";
import { formatBytes } from "../formatBytes";
import TargetSelectionTree from "../components/TargetSelectionTree.vue";
import CodeEditor from "../components/files/CodeEditor.vue";

// 平台 7 个（决策确认）：fabric/forge/neoforge/spigot/paper/velocity/bungee
const PLATFORMS = [
  { value: "fabric", label: "Fabric 模组" },
  { value: "forge", label: "Forge 模组" },
  { value: "neoforge", label: "NeoForge 模组" },
  { value: "spigot", label: "Spigot 插件" },
  { value: "paper", label: "Paper 插件" },
  { value: "velocity", label: "Velocity 插件" },
  { value: "bungee", label: "BungeeCord 插件" },
];
const MOD_PLATFORMS = ["fabric", "forge", "neoforge"];

// 内容包类型：单独配置（config） / 完全配置（full）
const CONTENT_TYPES = [
  { value: "config", label: "单独配置", hint: "zip 内 config/ → 服务端 config/，其余文件/文件夹按结构映射到工作目录对应位置" },
  { value: "full", label: "完全配置", hint: "zip 顶层即服务端目录内容（mods/、config/、world/、plugins/…），一次整包部署" },
];
const DEPLOY_TYPES = [
  { value: "jar", label: "插件 JAR" },
  { value: "config", label: "单独配置" },
  { value: "full", label: "完全配置" },
];

const loading = ref(false);
const submitting = ref(false);
const uploadProgress = ref({ active: false, loaded: 0, total: 0, percent: 0 });
const catalog = ref([]);
const nodeContents = ref([]);
const search = ref("");
// activePlatform 即当前平台 tab（上传平台固定为它）
const activePlatform = ref("spigot");
const uploadOpen = ref(false);
const deployOpen = ref(false);
const configOpen = ref(false);
const detailOpen = ref(false);
const detailForm = ref(null);
const contentOpen = ref(false);
const contentPlugin = ref(null);
const contentArtifact = ref(null);
// 内容包版本（GET /content → items），contentSelected 为树预览选中的版本。
const contentItems = ref([]);
const contentLoading = ref(false);
const contentSelected = ref(null);
const icons = ref({});
const uploadJAR = ref(null);
const uploadZip = ref(null);
// 上传模式：new（新建版本）/ edit（编辑指定制品的内容包）
const uploadMode = ref("new");
const editTarget = ref(null);
const uploadForm = ref({ pluginType: "spigot", pluginId: "", contentType: "config", autoInstall: false, contentName: "", contentVersion: "" });
const deployForm = ref({ pluginType: "spigot", pluginId: "", artifactId: null, deployType: "jar", rules: [], riskAcknowledged: false });
const configForm = ref({ pluginType: "spigot", pluginId: "", artifactId: null, rules: [] });
const configFiles = ref([]);
const configPath = ref("");
const configContent = ref("");
const savedConfigContent = ref("");
const configLoading = ref(false);

const canUpload = computed(() => hasPermission("plugin.upload"));
const canDeploy = computed(() => hasPermission("plugin.deploy"));
// 删除走 plugin.remove 权限（t2 约定；权限目录与 admin 组已含）。
const canRemove = computed(() => hasPermission("plugin.remove"));
const configDirty = computed(() => configPath.value && configContent.value !== savedConfigContent.value);
const isModPlatform = (type) => MOD_PLATFORMS.includes(type);

const filteredCatalog = computed(() => {
  const keyword = search.value.trim().toLowerCase();
  return catalog.value.filter((item) => {
    if (item.plugin_type !== activePlatform.value) return false;
    const artifact = currentArtifact(item);
    return !keyword || [item.name, item.plugin_id, artifact?.version, artifact?.main]
      .join(" ").toLowerCase().includes(keyword);
  });
});
const deployPlugin = computed(() => catalog.value.find((item) => (
  item.plugin_id === deployForm.value.pluginId && item.plugin_type === deployForm.value.pluginType
)));
const deployArtifact = computed(() => {
  const plugin = deployPlugin.value;
  if (!plugin || deployForm.value.artifactId == null) return null;
  return plugin.artifacts.find((item) => item.artifact_id === deployForm.value.artifactId) || null;
});
const configPlugin = computed(() => catalog.value.find((item) => (
  item.plugin_id === configForm.value.pluginId && item.plugin_type === configForm.value.pluginType
)));
// 内容包管理对话框：带内容包的制品版本（用于制品选择器）。
const contentArtifacts = computed(() => (
  (contentPlugin.value?.artifacts || []).filter((artifact) => contentInfo(artifact))
));
// isCurrentContent：判断内容包版本是否为该制品当前版本（manifest.content.content_id）。
function isCurrentContent(content) {
  const artifact = contentArtifact.value;
  return !!content && !!artifact?.content && content.content_id === artifact.content.content_id;
}
// shortHash：SHA256 短显示（完整值放 tooltip）。
function shortHash(sha) {
  return String(sha || "").slice(0, 12);
}
// modByID: 仓库内 mod 的 id（小写）→ 仓库条目，用于依赖"已收录"标记与跳转。
const modByID = computed(() => {
  const map = new Map();
  for (const item of catalog.value) {
    if (!isModPlatform(item.plugin_type)) continue;
    const id = modID(item);
    if (id) map.set(String(id).toLowerCase(), item);
  }
  return map;
});

function platformLabel(type) {
  return PLATFORMS.find((item) => item.value === type)?.label || type || "未知";
}

function currentArtifact(plugin) {
  return (plugin?.artifacts || []).find((item) => item.artifact_id === plugin.current_artifact_id)
    || plugin?.artifacts?.[0];
}

function modMeta(artifact) {
  return artifact?.mod_metadata || artifact?.descriptors?.fabric?.mod_metadata || null;
}

// modID 取仓库条目的 mod id：优先持久化元数据（fabric），其次遍历描述符
// 任意 key（fabric/forge/neoforge 的 descriptor map key 与平台一致，
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

// ---- 内容包（Content Bundle）----
// contentInfo 从制品解析内容包信息：优先 Manifest.Content{Type,Tree,SHA256,Files,Size}，
// 兼容旧版 ConfigSnapshot（present → 视为单独配置，无 Tree）。
function contentInfo(artifact) {
  const content = artifact?.content;
  if (content?.type === "config" || content?.type === "full") {
    return {
      type: content.type,
      sha256: content.sha256,
      files: content.files,
      size: content.size,
      tree: normalizeTree(content.tree),
    };
  }
  if (artifact?.config?.present) {
    return {
      type: "config", legacy: true,
      sha256: artifact.config.sha256, files: artifact.config.files, size: artifact.config.size,
      tree: [],
    };
  }
  return null;
}

function normalizeTree(tree) {
  if (Array.isArray(tree)) return tree.map(normalizeTreeEntry).filter(Boolean);
  if (tree && typeof tree === "object") {
    const list = tree.entries || tree.top || tree.items || tree.root || [];
    if (Array.isArray(list)) return list.map(normalizeTreeEntry).filter(Boolean);
  }
  return [];
}

function normalizeTreeEntry(entry) {
  if (typeof entry === "string") return { path: String(entry).replace(/^\/+/, "") };
  if (entry && typeof entry === "object") {
    const path = entry.path || entry.name || entry.dir || "";
    if (!path) return null;
    const normalized = { path: String(path).replace(/^\/+/, "") };
    if (entry.type) normalized.type = entry.type;
    if (Array.isArray(entry.children)) normalized.children = normalizeTree(entry.children);
    if (entry.files != null) normalized.files = entry.files;
    if (entry.size != null) normalized.size = entry.size;
    return normalized;
  }
  return null;
}

// flattenTree 把（可能嵌套的）结构树展开为带缩进深度的平铺列表，便于模板渲染。
function flattenTree(tree, depth = 0) {
  const out = [];
  for (const entry of tree || []) {
    out.push({ path: entry.path, type: entry.type, depth, files: entry.files, size: entry.size });
    if (entry.children?.length) out.push(...flattenTree(entry.children, depth + 1));
  }
  return out;
}

function contentTypeLabel(type) {
  if (type === "config") return "单独配置";
  if (type === "full") return "完全配置";
  return "无";
}

function contentTypeTagType(type) {
  if (type === "full") return "warning";
  if (type === "config") return "info";
  return "info";
}

function treeSummary(info) {
  if (!info) return "该制品未附加内容包";
  if (info.legacy) return "旧版配置快照（无结构信息）";
  const tree = info.tree || [];
  if (!tree.length) return "已附加内容包，暂无结构信息";
  return "顶层结构：" + tree.map((entry) => entry.path).join("、");
}

// availableDeployTypes 依据制品内容决定可用部署类型：
// 有 jar → 插件 JAR；内容包 type=config → 单独配置；type=full → 完全配置。
function availableDeployTypes(artifact) {
  const types = [];
  if (artifact?.artifact?.size) types.push(DEPLOY_TYPES[0]);
  const info = contentInfo(artifact);
  if (info?.type === "config") types.push(DEPLOY_TYPES[1]);
  if (info?.type === "full") types.push(DEPLOY_TYPES[2]);
  return types;
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
  if (!isModPlatform(activePlatform.value) || item.plugin_type !== activePlatform.value) {
    activePlatform.value = item.plugin_type;
  }
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
function handleZip(file) { uploadZip.value = file.raw || null; }
function clearJAR() { uploadJAR.value = null; }
function clearZip() { uploadZip.value = null; }

function resetUpload() {
  uploadMode.value = "new";
  editTarget.value = null;
  uploadJAR.value = null;
  uploadZip.value = null;
  uploadForm.value = {
    pluginType: activePlatform.value,
    pluginId: "",
    contentType: "config",
    autoInstall: false,
    contentName: "",
    contentVersion: "",
  };
}

async function uploadPlugin() {
  // 内容包模式（编辑配置 / 添加同种配置）：POST /content 新增内容包版本并标记 current。
  if (uploadMode.value === "content") {
    if (!uploadZip.value) {
      ElMessage.warning("请选择内容包 ZIP");
      return;
    }
    submitting.value = true;
    uploadProgress.value = { active: true, loaded: 0, total: uploadZip.value.size || 0, percent: 0 };
    try {
      const body = new FormData();
      body.append("content", uploadZip.value);
      body.append("content_type", uploadForm.value.contentType);
      // 与 t2 约定：POST .../{artifactID}/content（multipart content + content_type）
      await requestWithProgress(
        pluginArtifactPathOf(uploadForm.value.pluginType, uploadForm.value.pluginId, editTarget.value.artifact_id) + "/content",
        { method: "POST", body },
        (event) => { uploadProgress.value = { active: true, loaded: event.loaded, total: event.total, percent: event.total ? Math.round((event.loaded / event.total) * 100) : 0 }; },
      );
      uploadOpen.value = false;
      resetUpload();
      ElMessage.success("内容包版本已添加并设为当前");
      await load(true);
      await refreshContentDialog(editTarget.value);
    } catch (error) {
      ElMessage.error(error.message);
    } finally {
      submitting.value = false;
      uploadProgress.value.active = false;
    }
    return;
  }
  if (!uploadJAR.value && !uploadZip.value) {
    ElMessage.warning("请选择 JAR 文件或内容包 ZIP（至少一项）");
    return;
  }
  if (!uploadJAR.value && uploadZip.value && uploadForm.value.contentType === "config"
    && (!uploadForm.value.contentName.trim() || !uploadForm.value.contentVersion.trim())) {
    ElMessage.warning("纯内容包上传（无 JAR）需要填写名称与版本");
    return;
  }
  submitting.value = true;
  const body = new FormData();
  // 纯内容包上传时无 jar 字段
  if (uploadJAR.value) body.append("jar", uploadJAR.value);
  body.append("plugin_type", uploadForm.value.pluginType);
  body.append("auto_install", String(uploadForm.value.autoInstall));
  let expectedBytes = uploadJAR.value?.size || 0;
  if (uploadZip.value) {
    // 与 t2 约定：内容包 zip 用字段 content（旧 config 字段为 legacy 配置 zip）；
    // content_type 取 "config"（单独配置）| "full"（完全配置）；纯内容包身份用 name/version。
    body.append("content", uploadZip.value);
    body.append("content_type", uploadForm.value.contentType);
    if (uploadForm.value.contentName.trim()) body.append("name", uploadForm.value.contentName.trim());
    if (uploadForm.value.contentVersion.trim()) body.append("version", uploadForm.value.contentVersion.trim());
    expectedBytes += uploadZip.value.size || 0;
  }
  uploadProgress.value = { active: true, loaded: 0, total: expectedBytes, percent: 0 };
  try {
    const result = await requestWithProgress("/api/v1/plugins", { method: "POST", body }, (event) => {
      uploadProgress.value = { active: true, loaded: event.loaded, total: event.total, percent: event.total ? Math.round((event.loaded / event.total) * 100) : 0 };
    });
    uploadOpen.value = false;
    resetUpload();
    ElMessage.success(result.duplicate ? "制品已保存到仓库（内容与现有制品相同）" : "制品已保存到仓库");
    await load(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
    uploadProgress.value.active = false;
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
  const types = availableDeployTypes(artifact);
  deployForm.value = {
    pluginType: plugin.plugin_type,
    pluginId: plugin.plugin_id,
    artifactId: artifact?.artifact_id || null,
    deployType: types[0]?.value || "jar",
    rules: [],
    riskAcknowledged: false,
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

// 切换制品版本时，若当前部署类型在该制品上不可用，回退到首个可用类型。
watch(() => deployForm.value.artifactId, (id) => {
  if (id == null || !deployOpen.value) return;
  const artifact = deployPlugin.value?.artifacts?.find((item) => item.artifact_id === id);
  const types = availableDeployTypes(artifact);
  if (types.length && !types.some((item) => item.value === deployForm.value.deployType)) {
    deployForm.value.deployType = types[0].value;
  }
});

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

// ---- 仓库管理 API 路径（与 t2 约定已核对：删除/内容包版本/部署路径与 panel 路由一致）----
function pluginEntryPath(pluginType, pluginID) {
  return "/api/v1/plugins/" + encodeURIComponent(pluginType) + "/" + encodeURIComponent(pluginID);
}

function pluginArtifactPathOf(pluginType, pluginID, artifactID) {
  return pluginEntryPath(pluginType, pluginID) + "/" + encodeURIComponent(artifactID);
}

// deployEndpoint 依部署类型与制品内容形态决定调用路径（与 t2 约定）：
// jar → /deploy；新内容包模型（config/full）→ /content/deploy?kind=config|full；
// 旧版 ConfigSnapshot 制品（legacy）→ /config/deploy。
function deployEndpoint(form, artifact) {
  const base = pluginArtifactPath(form);
  if (form.deployType === "config") {
    return contentInfo(artifact)?.legacy
      ? base + "/config/deploy"
      : base + "/content/deploy?kind=config";
  }
  if (form.deployType === "full") return base + "/content/deploy?kind=full";
  return base + "/deploy";
}

function escapeHtml(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch]
  ));
}

// openUpload 打开上传对话框：平台固定为当前 tab（新建）或目标制品（编辑）。
function openUpload(options = {}) {
  const { mode = "new", artifact = null, contentType } = options;
  uploadMode.value = mode;
  editTarget.value = artifact;
  uploadJAR.value = null;
  uploadZip.value = null;
  uploadForm.value = {
    pluginType: artifact?.plugin_type || activePlatform.value,
    pluginId: artifact?.plugin_id || "",
    contentType: contentType || (artifact ? contentInfo(artifact)?.type : null) || "config",
    autoInstall: false,
    contentName: "",
    contentVersion: "",
  };
  uploadOpen.value = true;
}

// openContent 打开内容包管理对话框：制品选择 + 内容包版本列表（GET /content）+ 结构预览。
function openContent(plugin) {
  contentPlugin.value = plugin;
  contentOpen.value = true;
  const withContent = (plugin?.artifacts || []).filter((artifact) => contentInfo(artifact));
  selectContentArtifact(withContent.find((artifact) => artifact.artifact_id === plugin.current_artifact_id)
    || withContent[0] || plugin?.artifacts?.[0] || null);
}

// selectContentArtifact 切换内容包管理对话框的制品，并加载其内容包版本列表。
function selectContentArtifact(artifact) {
  contentArtifact.value = artifact || null;
  contentItems.value = [];
  contentSelected.value = null;
  if (artifact) loadContentItems();
}

// loadContentItems 拉取当前制品的内容包全部版本（升序），默认选中 current 版本。
async function loadContentItems() {
  const artifact = contentArtifact.value;
  const plugin = contentPlugin.value;
  if (!artifact || !plugin) return;
  contentLoading.value = true;
  try {
    const data = await request(
      pluginArtifactPathOf(plugin.plugin_type, plugin.plugin_id, artifact.artifact_id) + "/content",
    );
    contentItems.value = data.items || [];
    const currentId = artifact.content?.content_id;
    contentSelected.value = contentItems.value.find((item) => item.content_id === currentId)
      || contentItems.value[contentItems.value.length - 1] || null;
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    contentLoading.value = false;
  }
}

// addSameContent 添加同种配置：复用上传对话框（content 模式），预填当前制品内容包类型。
function addSameContent() {
  const artifact = contentArtifact.value;
  if (!artifact) return;
  const type = contentInfo(artifact)?.type || "config";
  contentOpen.value = false;
  openUpload({ mode: "content", artifact, contentType: type });
}

// editContent 编辑配置：复用上传对话框（content 模式）→ POST /content 新增版本并标记 current。
function editContent(plugin, artifact) {
  contentOpen.value = false;
  openUpload({ mode: "content", artifact, contentType: contentInfo(artifact)?.type || "config" });
}

// apiDelete 统一删除调用：命中 PLUGIN_DEPLOYED_CONFIRM_REQUIRED 时二次确认后带 confirm_deployed=true 重试；
// 用户取消二次确认返回 null，调用方据此中止。
async function apiDelete(path) {
  try {
    return await request(path, { method: "DELETE" });
  } catch (error) {
    if (error.code !== "PLUGIN_DEPLOYED_CONFIRM_REQUIRED") throw error;
    try {
      await ElMessageBox.confirm(
        "该条目存在部署偏好记录。删除仓库数据不会影响已部署到服务器上的文件，确定继续？",
        "已部署记录确认",
        { type: "warning", confirmButtonText: "确认删除", cancelButtonText: "取消", autofocus: false },
      );
    } catch {
      return null;
    }
    return request(path + (path.includes("?") ? "&" : "?") + "confirm_deployed=true", { method: "DELETE" });
  }
}

// removeEntry 删除整条仓库条目（高风险二次确认 + 已部署确认）。
async function removeEntry(plugin) {
  try {
    await ElMessageBox.confirm(
      `<div style="line-height:1.9">
        <p>确定删除仓库条目 <strong>${escapeHtml(plugin.name || plugin.plugin_id)}</strong>？</p>
        <ul style="padding-left:20px;margin:8px 0">
          <li>该条目下的 <strong>${plugin?.artifacts?.length || 0} 个制品版本</strong>（含内容包）将被全部删除</li>
          <li>仅删除仓库数据，<strong>不影响</strong>已部署到服务器上的文件</li>
        </ul>
      </div>`,
      "删除仓库条目 — 高风险确认",
      { dangerouslyUseHTMLString: true, type: "warning", confirmButtonText: "确认删除", cancelButtonText: "取消", autofocus: false },
    );
  } catch {
    return;
  }
  submitting.value = true;
  try {
    const result = await apiDelete(pluginEntryPath(plugin.plugin_type, plugin.plugin_id));
    if (result === null) return;
    ElMessage.success("仓库条目已删除");
    await load(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

// removeArtifact 删除单个制品版本（含其内容包，高风险二次确认 + 已部署确认）。
async function removeArtifact(plugin, artifact) {
  const isLast = (plugin?.artifacts || []).length <= 1;
  try {
    await ElMessageBox.confirm(
      `<div style="line-height:1.9">
        <p>确定删除制品版本 <strong>${escapeHtml(artifact.version)}</strong>（#${artifact.artifact_id}）？</p>
        <ul style="padding-left:20px;margin:8px 0">
          <li>该版本的 JAR 与内容包将被<strong>删除</strong>${isLast ? "；这是该条目<strong>最后一个版本</strong>，删除后条目将消失" : ""}</li>
          <li>不影响已部署到服务器上的文件</li>
        </ul>
      </div>`,
      "删除制品版本 — 高风险确认",
      { dangerouslyUseHTMLString: true, type: "warning", confirmButtonText: "确认删除", cancelButtonText: "取消", autofocus: false },
    );
  } catch {
    return;
  }
  submitting.value = true;
  try {
    const result = await apiDelete(pluginArtifactPathOf(plugin.plugin_type, plugin.plugin_id, artifact.artifact_id));
    if (result === null) return;
    ElMessage.success(result?.removed_plugin ? "制品版本已删除，条目已移除" : "制品版本已删除");
    await load(true);
    refreshContentDialog(plugin);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

// removeContent 删除一个内容包版本（按 content_id；删 current 自动回退到剩余最高版本）。
async function removeContent(plugin, artifact, content) {
  try {
    await ElMessageBox.confirm(
      `<div style="line-height:1.9">
        <p>确定删除内容包版本 <strong>#${content.content_id}</strong>（${contentTypeLabel(content.type)}）？</p>
        <ul style="padding-left:20px;margin:8px 0">
          <li>该版本的内容包将被<strong>删除</strong>${isCurrentContent(content) ? "；这是<strong>当前版本</strong>，删除后自动回退到剩余最高版本" : ""}</li>
          <li>不影响已部署到服务器上的文件</li>
        </ul>
      </div>`,
      "删除内容包版本 — 高风险确认",
      { dangerouslyUseHTMLString: true, type: "warning", confirmButtonText: "确认删除", cancelButtonText: "取消", autofocus: false },
    );
  } catch {
    return;
  }
  submitting.value = true;
  try {
    const result = await apiDelete(
      pluginArtifactPathOf(plugin.plugin_type, plugin.plugin_id, artifact.artifact_id) + "/content/" + content.content_id,
    );
    if (result === null) return;
    ElMessage.success("内容包版本已删除");
    await load(true);
    await refreshContentDialog(plugin);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

// removeAllContent 删除某制品的全部内容包版本（DELETE .../content，保留 JAR）。
async function removeAllContent(plugin, artifact) {
  const count = contentItems.value.length;
  try {
    await ElMessageBox.confirm(
      `<div style="line-height:1.9">
        <p>确定删除制品 <strong>${escapeHtml(artifact.version)}</strong>（#${artifact.artifact_id}）的<strong>全部 ${count} 个内容包版本</strong>？</p>
        <ul style="padding-left:20px;margin:8px 0">
          <li>该制品的 JAR 将<strong>保留</strong>，仅清空内容包（单独/完全配置）</li>
          <li>不影响已部署到服务器上的文件</li>
        </ul>
      </div>`,
      "删除全部内容包版本 — 高风险确认",
      { dangerouslyUseHTMLString: true, type: "warning", confirmButtonText: "确认删除", cancelButtonText: "取消", autofocus: false },
    );
  } catch {
    return;
  }
  submitting.value = true;
  try {
    const result = await apiDelete(
      pluginArtifactPathOf(plugin.plugin_type, plugin.plugin_id, artifact.artifact_id) + "/content",
    );
    if (result === null) return;
    ElMessage.success("该制品的内容包版本已全部删除");
    await load(true);
    await refreshContentDialog(plugin);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

// setCurrentArtifact 切换条目的当前制品版本（POST .../current，t2 已实现；plugin.upload）。
async function setCurrentArtifact(plugin, artifact) {
  if (!artifact || artifact.artifact_id === plugin.current_artifact_id) return;
  submitting.value = true;
  try {
    await request(pluginArtifactPathOf(plugin.plugin_type, plugin.plugin_id, artifact.artifact_id) + "/current", { method: "POST" });
    ElMessage.success("已切换当前版本为 " + artifact.version + " #" + artifact.artifact_id);
    await load(true);
    await refreshContentDialog(plugin);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

// refreshContentDialog 内容包管理对话框在变更后按最新目录数据刷新选中状态。
async function refreshContentDialog(plugin) {
  if (!contentOpen.value) return;
  const fresh = catalog.value.find((item) => (
    item.plugin_id === plugin.plugin_id && item.plugin_type === plugin.plugin_type
  ));
  if (!fresh) {
    contentOpen.value = false;
    return;
  }
  contentPlugin.value = fresh;
  const artifacts = fresh.artifacts || [];
  const stillThere = artifacts.find((artifact) => artifact.artifact_id === contentArtifact.value?.artifact_id);
  const target = stillThere
    || artifacts.find((artifact) => artifact.artifact_id === fresh.current_artifact_id)
    || artifacts[0] || null;
  if (target !== contentArtifact.value) selectContentArtifact(target);
  else await loadContentItems();
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

// deployTargetLabel 将部署结果目标（node_id/server_id）映射为「节点/服务器」展示名。
function deployTargetLabel(target) {
  const node = nodeContents.value.find((item) => item?.node?.id === target.node_id);
  const nodeName = node?.node?.name || target.node_id || "未知节点";
  if (!node) return nodeName + "/" + (target.server_id || "未知目标");
  const server = (node.servers || []).find((item) => item.server_id === target.server_id);
  const serverName = server ? server.name : (target.server_id || "未知目标");
  return nodeName + "/" + serverName;
}

// deployResultMessage 汇总部署结果：成功→success；部分失败→warning 并列出失败目标；全失败→error。
// successPrefix 为全部成功时的完整动词短语（如「配置已部署到」），默认「已部署到」。
function deployResultMessage(prefix, targets, successPrefix) {
  const failed = (targets || []).filter((item) => item.error);
  if (!failed.length) {
    ElMessage.success((successPrefix || "已部署到 ") + (targets?.length || 0) + " 个服务器");
    return;
  }
  const okCount = (targets?.length || 0) - failed.length;
  const detail = failed.slice(0, 5)
    .map((item) => deployTargetLabel(item) + ": " + item.error)
    .join("；");
  const more = failed.length > 5 ? "；等 " + failed.length + " 项" : "";
  const message = prefix + "完成：" + okCount + " 个目标成功，" + failed.length + " 个目标失败"
    + (detail ? "（" + detail + more + "）" : "");
  if (okCount === 0) ElMessage.error(message);
  else ElMessage.warning(message);
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
    deployResultMessage("配置部署", result.targets, "配置已部署到 ");
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

// 完全配置部署的高风险确认文案：覆盖同名+保留额外、自动备份、失败回滚。
const fullDeployRiskHTML = `
<div style="line-height:1.9">
  <p><strong>完全配置部署将把内容包整包解压到目标实例的工作目录：</strong></p>
  <ul style="padding-left:20px;margin:10px 0">
    <li>zip 内同名文件/目录将被<strong>覆盖</strong>；zip 未包含的现有文件<strong>保留</strong>（覆盖同名 + 保留额外）</li>
    <li>内容包可能包含 <code>world/</code> 等大型目录，部署耗时较长</li>
    <li>部署前会对目标工作目录做<strong>自动备份</strong>，失败时可回滚</li>
    <li>建议先停止目标服务器再进行部署</li>
  </ul>
</div>`;

async function deploy() {
  if (!deployForm.value.artifactId) {
    ElMessage.warning("请选择制品版本");
    return;
  }
  if (deployForm.value.deployType === "full") {
    if (!deployForm.value.riskAcknowledged) {
      ElMessage.warning("完全配置部署为高风险操作，请先勾选风险确认");
      return;
    }
    try {
      await ElMessageBox.confirm(fullDeployRiskHTML, "完全配置部署 — 高风险确认", {
        dangerouslyUseHTMLString: true,
        type: "warning",
        confirmButtonText: "确认部署",
        cancelButtonText: "取消",
        autofocus: false,
      });
    } catch {
      return;
    }
  }
  submitting.value = true;
  try {
    // 与 t2 最终契约：新版内容包（config|full）统一走 .../content/deploy，
    // 类型字段 body content_type（?kind= 等价，双保险）；旧版 ConfigSnapshot 制品仍走 /config/deploy。
    // 完全配置（high-risk 确认后）额外传 backup_snapshot=true，daemon 部署前整目录快照备份并回传 backup_path。
    const path = deployEndpoint(deployForm.value, deployArtifact.value);
    const body = { rules: deployForm.value.rules };
    if (deployForm.value.deployType === "config" || deployForm.value.deployType === "full") {
      body.content_type = deployForm.value.deployType;
    }
    if (deployForm.value.deployType === "full") body.backup_snapshot = true;
    const result = await request(path, { method: "POST", body: JSON.stringify(body) });
    const targets = result.targets || [];
    const failed = targets.filter((item) => item.error);
    if (failed.length) {
      deployResultMessage("部署", targets);
    } else if (deployForm.value.deployType === "config" || deployForm.value.deployType === "full") {
      // 内容包部署：daemon 每目标回传 applied/overwritten/added，汇总提示。
      const changed = targets.reduce((acc, item) => {
        const data = item.data || {};
        acc.applied += Number(data.applied) || 0;
        acc.overwritten += Number(data.overwritten) || 0;
        acc.added += Number(data.added) || 0;
        return acc;
      }, { applied: 0, overwritten: 0, added: 0 });
      if (changed.applied || changed.overwritten || changed.added) {
        ElMessage.success("已部署到 " + targets.length + " 个服务器（应用 " + changed.applied
          + "，覆盖 " + changed.overwritten + "，新增 " + changed.added + "）");
      } else {
        ElMessage.success("已部署到 " + targets.length + " 个服务器");
      }
    } else {
      ElMessage.success("已部署到 " + targets.length + " 个服务器");
    }
    // 完全配置部署：daemon 部署前做整目录快照备份并回传 backup_path，供失败回滚。
    if (deployForm.value.deployType === "full") {
      const backups = targets
        .filter((item) => item.data?.backup_path)
        .map((item) => item.data.backup_path);
      if (backups.length) {
        ElMessage.info("已生成工作目录快照备份（失败可回滚）：\n" + backups.join("\n"));
      }
    }
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
      <div><h2>仓库</h2><p>{{ catalog.length }} 个制品 · 本地文件为权威数据</p></div>
      <div class="toolbar-actions">
        <el-tooltip content="刷新">
          <el-button class="square-button" :loading="loading" aria-label="刷新" @click="load()">
            <RefreshCw v-if="!loading" :size="16" />
          </el-button>
        </el-tooltip>
        <el-button v-if="canUpload" :loading="submitting" @click="rescan">
          <ArchiveRestore :size="16" />重新扫描
        </el-button>
        <el-button v-if="canUpload" type="primary" @click="openUpload">
          <Plus :size="16" />上传
        </el-button>
      </div>
    </div>

    <el-tabs v-model="activePlatform" class="repo-tabs">
      <el-tab-pane
        v-for="platform in PLATFORMS"
        :key="platform.value"
        :label="platform.label"
        :name="platform.value"
      />
    </el-tabs>

    <div class="table-toolbar">
      <el-input
        v-model="search"
        class="search-input"
        clearable
        placeholder="搜索名称、ID、版本或主类"
      >
        <template #prefix><Search :size="15" /></template>
      </el-input>
      <span class="table-toolbar-note">{{ platformLabel(activePlatform) }} · {{ filteredCatalog.length }} 个制品</span>
    </div>

    <div v-loading="loading" class="table-frame plugin-repository-table">
      <el-table :data="filteredCatalog" :row-key="(row) => row.plugin_type + ':' + row.plugin_id">
        <el-table-column type="expand">
          <template #default="{ row }">
            <div class="artifact-list">
              <div v-for="artifact in row.artifacts" :key="artifact.artifact_id" class="artifact-row">
                <div class="artifact-row-main">
                  <div class="artifact-title">
                    <strong>{{ artifact.version }}</strong><code>#{{ artifact.artifact_id }}</code>
                    <el-tag v-if="artifact.artifact_id === row.current_artifact_id" type="success" effect="plain">当前</el-tag>
                  </div>
                  <span class="artifact-filename">{{ artifact.artifact.original_filename || "纯内容包（无 JAR）" }}</span>
                </div>
                <span>{{ fileSize(artifact.artifact.size) }}</span>
                <span>{{ formatDate(artifact.uploaded_at) }}</span>
                <span>{{ artifact.uploaded_by.display_name || artifact.uploaded_by.username || "本地导入" }}</span>
                <el-tag :type="contentTypeTagType(contentInfo(artifact)?.type)" effect="plain">
                  {{ contentTypeLabel(contentInfo(artifact)?.type) }}
                </el-tag>
                <el-button v-if="artifact.config?.present && (canUpload || canDeploy)" size="small" plain @click="openConfig(row, artifact)">
                  <FileCode2 :size="14" />配置
                </el-button>
                <el-button v-if="canDeploy" size="small" plain @click="openDeploy(row, artifact)">
                  <Upload :size="14" />部署
                </el-button>
                <el-button v-if="contentInfo(artifact) && canUpload" size="small" plain @click="editContent(row, artifact)">
                  <FileCode2 :size="14" />编辑配置
                </el-button>
                <!-- 设为当前（回滚）：POST .../current，切换条目 current 制品版本（t2 端点，plugin.upload） -->
                <el-button v-if="canUpload && artifact.artifact_id !== row.current_artifact_id" size="small" plain @click="setCurrentArtifact(row, artifact)">
                  <CheckCircle2 :size="14" />设为当前
                </el-button>
                <el-button v-if="canRemove" size="small" plain type="danger" @click="removeArtifact(row, artifact)">
                  <Trash2 :size="14" />删除版本
                </el-button>
              </div>
              <div v-if="contentInfo(currentArtifact(row))?.tree?.length" class="content-tree-preview">
                <div class="content-tree-head"><FolderOpen :size="14" />内容包结构（顶层目录树）</div>
                <div class="content-tree-grid">
                  <div
                    v-for="entry in flattenTree(contentInfo(currentArtifact(row)).tree)"
                    :key="entry.depth + ':' + entry.path"
                    class="content-tree-entry"
                    :style="{ paddingLeft: (10 + entry.depth * 16) + 'px' }"
                  >
                    <FolderOpen :size="13" class="content-tree-icon" />
                    <code>{{ entry.path }}</code>
                    <small v-if="entry.files != null">{{ entry.files }} 文件</small>
                    <small v-if="entry.type !== 'dir' && entry.size != null">{{ fileSize(entry.size) }}</small>
                  </div>
                </div>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="名称" min-width="240">
          <template #default="{ row }">
            <div class="node-cell">
              <span class="mod-icon-wrap">
                <img v-if="isModPlatform(row.plugin_type) && modIcon(row)" :src="modIcon(row)" class="mod-icon" alt="" />
                <Package v-else :size="18" />
              </span>
              <div>
                <strong>{{ row.name }}</strong>
                <small v-if="isModPlatform(row.plugin_type)"><code>{{ modID(row) }}</code> · {{ currentArtifact(row)?.version || "-" }}</small>
                <small v-else>{{ currentArtifact(row)?.main || row.plugin_id }}</small>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="平台" width="130">
          <template #default="{ row }">
            <el-tag effect="plain">{{ platformLabel(row.plugin_type) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="isModPlatform(activePlatform)" label="环境" width="90">
          <template #default="{ row }">
            <el-tag :type="environmentTagType(modMeta(currentArtifact(row))?.environment)" effect="plain">
              {{ environmentLabel(modMeta(currentArtifact(row))?.environment) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="内容包" width="110">
          <template #default="{ row }">
            <el-tooltip :content="treeSummary(contentInfo(currentArtifact(row)))" placement="top">
              <el-tag :type="contentTypeTagType(contentInfo(currentArtifact(row))?.type)" effect="plain">
                {{ contentTypeLabel(contentInfo(currentArtifact(row))?.type) }}
              </el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="当前版本" width="130">
          <template #default="{ row }"><code>{{ currentArtifact(row)?.version || "-" }}</code></template>
        </el-table-column>
        <el-table-column label="作者" min-width="150">
          <template #default="{ row }">{{ currentArtifact(row)?.authors?.join(", ") || "-" }}</template>
        </el-table-column>
        <el-table-column v-if="isModPlatform(activePlatform)" label="依赖" width="120">
          <template #default="{ row }">
            <span v-if="dependencyCounts(modMeta(currentArtifact(row))).depends" class="mod-dep-count">
              必装 {{ dependencyCounts(modMeta(currentArtifact(row))).depends }}
              <small v-if="dependencyCounts(modMeta(currentArtifact(row))).suggests">+ 建议 {{ dependencyCounts(modMeta(currentArtifact(row))).suggests }}</small>
            </span>
            <span v-else class="mod-dep-count muted">无必装依赖</span>
          </template>
        </el-table-column>
        <el-table-column v-if="isModPlatform(activePlatform)" label="已收录" width="100">
          <template #default="{ row }">
            <template v-if="dependencyCounts(modMeta(currentArtifact(row))).depends">
              <el-tag :type="dependencyCounts(modMeta(currentArtifact(row))).collected === dependencyCounts(modMeta(currentArtifact(row))).depends ? 'success' : 'warning'" effect="plain">
                {{ dependencyCounts(modMeta(currentArtifact(row))).collected }}/{{ dependencyCounts(modMeta(currentArtifact(row))).depends }}
              </el-tag>
            </template>
            <span v-else class="table-muted">-</span>
          </template>
        </el-table-column>
        <el-table-column label="新服安装" width="90">
          <template #default="{ row }">
            <el-tag :type="row.auto_install ? 'success' : 'info'" effect="plain">
              {{ row.auto_install ? "自动" : "手动" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="制品" width="80"><template #default="{ row }">{{ row.artifacts?.length || 0 }}</template></el-table-column>
        <el-table-column label="操作" :width="isModPlatform(activePlatform) ? 300 : 260" align="right">
          <template #default="{ row }">
            <el-button v-if="isModPlatform(activePlatform)" type="primary" link @click="openDetail(row)"><Info :size="14" />详情</el-button>
            <el-button type="primary" link @click="openContent(row)"><Boxes :size="14" />内容包</el-button>
            <el-button v-if="canDeploy" type="primary" link @click="openDeploy(row)"><Upload :size="14" />部署</el-button>
            <el-button v-if="canRemove" type="danger" link @click="removeEntry(row)"><Trash2 :size="14" />删除</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <div class="table-empty">
            <Package :size="24" />
            <span>{{ platformLabel(activePlatform) }} 暂无制品，点击右上角「上传」添加</span>
          </div>
        </template>
      </el-table>
    </div>
  </div>

  <el-dialog
    :title="uploadMode === 'content'
      ? '内容包版本 · ' + (editTarget?.version || '') + ' #' + (editTarget?.artifact_id || '')
      : '上传制品 · ' + platformLabel(uploadForm.pluginType || activePlatform)"
    width="min(600px, 94vw)"
    @closed="resetUpload"
    v-model="uploadOpen"
  >
    <el-form label-position="top">
      <div class="dialog-form-grid">
        <el-form-item :label="uploadMode === 'content' ? '目标制品' : '平台'" required>
          <el-input
            :model-value="uploadMode === 'content'
              ? editTarget?.version + '  #' + editTarget?.artifact_id
              : platformLabel(uploadForm.pluginType || activePlatform)"
            disabled
          />
        </el-form-item>
        <el-form-item v-if="uploadMode !== 'content'" label="新服务器自动安装">
          <el-switch v-model="uploadForm.autoInstall" />
        </el-form-item>
      </div>
      <el-form-item v-if="uploadMode !== 'content'" label="插件 / Mod JAR（可选）">
        <el-upload :auto-upload="false" :limit="1" accept=".jar" :on-change="handleJAR" :on-remove="clearJAR">
          <el-button><Package :size="15" />选择 JAR</el-button>
        </el-upload>
      </el-form-item>
      <el-form-item :label="uploadMode === 'content' ? '内容包 ZIP（新增版本并设为当前）' : '内容包 ZIP（可选）'" required>
        <el-upload :auto-upload="false" :limit="1" accept=".zip" :on-change="handleZip" :on-remove="clearZip">
          <el-button><Boxes :size="15" />选择 ZIP</el-button>
        </el-upload>
        <div class="form-hint">zip 顶层即服务端目录结构（config/、plugins/、mods/、world/…），不自动剥离外层目录</div>
      </el-form-item>
      <div v-if="uploadMode !== 'content' && !uploadJAR" class="dialog-form-grid">
        <el-form-item label="名称" :required="uploadZip && uploadForm.contentType === 'config'">
          <el-input v-model="uploadForm.contentName" maxlength="100" placeholder="纯内容包上传时填写" />
        </el-form-item>
        <el-form-item label="版本" :required="uploadZip && uploadForm.contentType === 'config'">
          <el-input v-model="uploadForm.contentVersion" maxlength="64" placeholder="如 1.0.0" />
        </el-form-item>
      </div>
      <div v-if="uploadMode !== 'content' && !uploadJAR && uploadZip" class="form-hint">
        {{ uploadForm.contentType === 'config' ? '单独配置内容包需要名称与版本（后端据此建立仓库条目）' : '完全配置内容包可留空名称/版本，后端将扫描 zip 内可识别 JAR 推导身份' }}
      </div>
      <el-form-item v-if="uploadZip" label="内容包类型" required>
        <el-radio-group v-model="uploadForm.contentType">
          <el-radio v-for="type in CONTENT_TYPES" :key="type.value" :value="type.value">{{ type.label }}</el-radio>
        </el-radio-group>
        <div class="form-hint">
          <template v-if="uploadForm.contentType === 'config'">单独配置：zip 内 config/ → 服务端 config/，其余按结构映射；与 JAR 分开部署</template>
          <template v-else>完全配置：zip 顶层即服务端目录内容，整包一次部署（可含 world/ 等大目录）</template>
        </div>
      </el-form-item>
    </el-form>
    <div v-if="uploadProgress.active" class="repo-upload-progress">
      <div>
        <span>上传中 {{ uploadProgress.percent }}%（{{ formatBytes(uploadProgress.loaded) }} / {{ formatBytes(uploadProgress.total) }}）</span>
      </div>
      <el-progress
        :percentage="uploadProgress.percent"
        :indeterminate="!uploadProgress.total"
        :stroke-width="4"
        :show-text="false"
      />
    </div>
    <template #footer>
      <el-button @click="uploadOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="uploadPlugin">
        {{ uploadMode === 'content' ? '添加版本' : '上传' }}
      </el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="contentOpen" title="内容包管理" width="min(860px, 96vw)">
    <template v-if="contentPlugin">
      <div class="content-manage-head">
        <div class="content-manage-title">
          <strong>{{ contentPlugin.name }}</strong>
          <code>{{ contentPlugin.plugin_id }} · {{ platformLabel(contentPlugin.plugin_type) }}</code>
        </div>
        <el-button v-if="canUpload" type="primary" plain size="small" @click="addSameContent">
          <Plus :size="14" />添加同种配置
        </el-button>
      </div>

      <el-form label-position="top" class="content-artifact-select">
        <el-form-item label="制品版本" required>
          <div class="content-artifact-row">
            <el-select v-model="contentArtifact" class="full-control" @change="selectContentArtifact">
              <el-option
                v-for="artifact in contentPlugin.artifacts"
                :key="artifact.artifact_id"
                :label="artifact.version + '  #' + artifact.artifact_id
                  + (contentInfo(artifact) ? '（' + contentTypeLabel(contentInfo(artifact).type) + '）' : '（无内容包）')"
                :value="artifact"
              />
            </el-select>
            <el-button
              v-if="canUpload && contentArtifact && contentArtifact.artifact_id !== contentPlugin.current_artifact_id"
              plain
              size="small"
              @click="setCurrentArtifact(contentPlugin, contentArtifact)"
            >设为当前</el-button>
          </div>
        </el-form-item>
      </el-form>

      <div class="content-version-head">
        <span>内容包版本（{{ contentItems.length }}）</span>
        <el-button
          v-if="canRemove && contentItems.length"
          size="small"
          link
          type="danger"
          @click="removeAllContent(contentPlugin, contentArtifact)"
        >删除全部版本</el-button>
      </div>

      <div v-loading="contentLoading" class="content-version-list">
        <template v-if="contentItems.length">
          <div
            v-for="item in contentItems"
            :key="item.content_id"
            class="content-version-row"
            :class="{ active: contentSelected?.content_id === item.content_id }"
            @click="contentSelected = item"
          >
            <div class="content-version-main">
              <strong>#{{ item.content_id }}</strong>
              <el-tag :type="contentTypeTagType(item.type)" effect="plain" size="small">{{ contentTypeLabel(item.type) }}</el-tag>
              <el-tag v-if="isCurrentContent(item)" type="success" effect="plain" size="small">当前</el-tag>
            </div>
            <div class="content-version-meta">
              <span v-if="item.files != null">{{ item.files }} 文件</span>
              <span v-if="item.size != null">{{ fileSize(item.size) }}</span>
              <el-tooltip v-if="item.sha256" :content="item.sha256" placement="top">
                <code class="content-sha">{{ shortHash(item.sha256) }}</code>
              </el-tooltip>
            </div>
            <div class="content-version-actions" @click.stop>
              <el-button v-if="canUpload" size="small" link @click="editContent(contentPlugin, contentArtifact)">编辑配置</el-button>
              <el-button v-if="canRemove" size="small" link type="danger" @click="removeContent(contentPlugin, contentArtifact, item)">删除</el-button>
            </div>
          </div>
        </template>
        <div v-else-if="!contentLoading" class="content-manage-empty">
          {{ contentArtifact ? '该制品没有内容包版本。可在上传制品时附带内容包 ZIP，或点击「添加同种配置」。' : '该条目没有制品。' }}
        </div>
      </div>

      <div v-if="contentSelected?.tree?.length" class="content-manage-tree">
        <div class="content-tree-head">
          <FolderOpen :size="14" />内容包结构（#{{ contentSelected.content_id }} {{ contentTypeLabel(contentSelected.type) }}）
        </div>
        <div class="content-tree-grid">
          <div
            v-for="entry in flattenTree(contentSelected.tree)"
            :key="entry.depth + ':' + entry.path"
            class="content-tree-entry"
            :style="{ paddingLeft: (10 + entry.depth * 16) + 'px' }"
          >
            <FolderOpen :size="13" class="content-tree-icon" />
            <code>{{ entry.path }}</code>
            <small v-if="entry.files != null">{{ entry.files }} 文件</small>
            <small v-if="entry.type !== 'dir' && entry.size != null">{{ fileSize(entry.size) }}</small>
          </div>
        </div>
      </div>
    </template>
    <template #footer>
      <el-button @click="contentOpen = false">关闭</el-button>
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
            <el-tag :type="contentTypeTagType(contentInfo(detailForm.artifact)?.type)" effect="plain">
              内容包：{{ contentTypeLabel(contentInfo(detailForm.artifact)?.type) }}
            </el-tag>
          </div>
        </div>
      </div>

      <p v-if="detailForm.artifact.description" class="mod-detail-description">{{ detailForm.artifact.description }}</p>
      <p v-else class="mod-detail-description muted">该 Mod 未提供描述。</p>

      <div v-if="contentInfo(detailForm.artifact)?.tree?.length" class="mod-detail-section">
        <h4>内容包结构（顶层目录树）</h4>
        <div class="content-tree-grid detail">
          <div
            v-for="entry in flattenTree(contentInfo(detailForm.artifact).tree)"
            :key="entry.depth + ':' + entry.path"
            class="content-tree-entry"
            :style="{ paddingLeft: (10 + entry.depth * 16) + 'px' }"
          >
            <FolderOpen :size="13" class="content-tree-icon" />
            <code>{{ entry.path }}</code>
            <small v-if="entry.files != null">{{ entry.files }} 文件</small>
            <small v-if="entry.size != null">{{ fileSize(entry.size) }}</small>
          </div>
        </div>
      </div>

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

  <el-dialog v-model="deployOpen" title="部署制品" width="min(780px, 94vw)">
    <el-form label-position="top">
      <el-form-item label="制品"><el-input :model-value="deployPlugin?.name" disabled /></el-form-item>
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
      <el-form-item label="部署类型" required>
        <el-radio-group v-model="deployForm.deployType">
          <el-radio v-for="type in availableDeployTypes(deployArtifact)" :key="type.value" :value="type.value">
            {{ type.label }}
          </el-radio>
        </el-radio-group>
        <div class="form-hint">
          <template v-if="deployForm.deployType === 'jar'">仅部署 JAR 到目标实例的制品目录</template>
          <template v-else-if="deployForm.deployType === 'config'">仅部署内容包（单独配置），按结构映射到工作目录</template>
          <template v-else>整包部署内容包（含 mods/config/world/plugins 等）到工作目录 — 高风险</template>
        </div>
      </el-form-item>
      <el-alert
        v-if="deployForm.deployType === 'full'"
        type="warning"
        :closable="false"
        show-icon
        class="full-risk-alert"
        title="完全配置部署为高风险操作"
      >
        <div class="full-risk-text">
          <div><ShieldAlert :size="14" />将覆盖目标工作目录中的同名文件，可能包含 world/ 等大型目录</div>
          <div><AlertTriangle :size="14" />部署前自动备份目标工作目录，失败时可回滚</div>
          <div><AlertTriangle :size="14" />建议先停止目标服务器</div>
        </div>
      </el-alert>
      <el-form-item v-if="deployForm.deployType === 'full'" required>
        <el-checkbox v-model="deployForm.riskAcknowledged">我已知晓覆盖与备份/回滚说明，确认执行完全配置部署</el-checkbox>
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

  <el-dialog v-model="configOpen" title="编辑制品配置" width="min(1080px, 96vw)" destroy-on-close>
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
.artifact-row-main { display: flex; min-width: 0; flex-direction: column; gap: 3px; }
.artifact-title { display: flex; align-items: center; gap: 8px; }
.artifact-title code { color: var(--app-text-muted); font-size: 12px; }
.artifact-filename { overflow: hidden; color: var(--app-text-secondary); font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.content-tree-preview { margin-top: 6px; border: 1px dashed var(--app-border); border-radius: 6px; padding: 8px 10px; background: var(--app-surface-muted); }
.content-tree-head { display: flex; align-items: center; gap: 6px; margin-bottom: 6px; color: var(--app-text-secondary); font-size: 12px; }
.content-tree-grid { display: flex; flex-direction: column; gap: 2px; }
.content-tree-grid.detail { margin-top: 2px; }
.content-tree-entry { display: flex; align-items: center; gap: 8px; min-height: 24px; }
.content-tree-entry code { font: 12px/1.4 Consolas, monospace; color: var(--app-text); }
.content-tree-entry small { color: var(--app-text-muted); font-size: 11px; }
.content-tree-icon { flex: none; color: var(--app-text-muted); }
.form-hint { width: 100%; margin-top: 4px; color: var(--app-text-muted); font-size: 12px; line-height: 1.6; }
.repo-upload-progress { display: grid; gap: 5px; margin-bottom: 14px; border: 1px solid var(--app-border); border-radius: 6px; padding: 8px 10px; background: var(--app-surface-muted); }
.repo-upload-progress > div { display: flex; justify-content: space-between; gap: 12px; color: var(--app-text-muted); font-size: 11px; }
.repo-upload-progress span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.full-risk-alert { margin-bottom: 14px; }
.full-risk-text { display: flex; flex-direction: column; gap: 4px; font-size: 12px; }
.full-risk-text div { display: flex; align-items: center; gap: 6px; }
.table-toolbar-note { color: var(--app-text-muted); font-size: 12px; }
.content-manage-head { display: flex; align-items: center; gap: 12px; margin-bottom: 12px; }
.content-manage-title { display: flex; min-width: 0; flex-direction: column; gap: 2px; flex: 1; }
.content-manage-title strong { color: var(--app-text); }
.content-manage-title code { color: var(--app-text-muted); font-size: 12px; }
.content-artifact-select { margin-bottom: 10px; }
.content-artifact-row { display: flex; width: 100%; align-items: center; gap: 8px; }
.content-artifact-row .full-control { flex: 1; }
.content-version-head { display: flex; align-items: center; justify-content: space-between; margin: 4px 0 6px; color: var(--app-text-secondary); font-size: 12px; }
.content-sha { color: var(--app-text-muted); font-size: 11px; }
.content-version-list { display: flex; flex-direction: column; gap: 4px; border: 1px solid var(--app-border); border-radius: 6px; padding: 6px; }
.content-version-row { display: flex; align-items: center; gap: 10px; border: 1px solid transparent; border-radius: 4px; padding: 8px 10px; cursor: pointer; }
.content-version-row:hover { background: var(--app-surface-hover); }
.content-version-row.active { border-color: var(--app-border); background: var(--app-surface-muted); }
.content-version-main { display: flex; min-width: 0; align-items: center; gap: 8px; flex: 1; }
.content-version-main code { color: var(--app-text-muted); font-size: 12px; }
.content-version-meta { display: flex; align-items: center; gap: 10px; color: var(--app-text-muted); font-size: 12px; }
.content-version-actions { display: flex; align-items: center; gap: 2px; }
.content-manage-empty { border: 1px dashed var(--app-border); border-radius: 6px; padding: 22px; text-align: center; color: var(--app-text-muted); font-size: 12px; }
.content-manage-tree { margin-top: 12px; border: 1px dashed var(--app-border); border-radius: 6px; padding: 10px; background: var(--app-surface-muted); }
@media (max-width: 720px) { .plugin-config-editor { min-height: 580px; grid-template-columns: 1fr; grid-template-rows: minmax(108px, auto) minmax(0, 1fr); } .plugin-config-files { max-height: 150px; border-right: 0; border-bottom: 1px solid var(--app-border); overflow-y: auto; } .plugin-config-content :deep(.code-editor), .plugin-config-content :deep(.cm-editor), .plugin-config-content :deep(.cm-scroller) { min-height: 390px; } }
</style>
