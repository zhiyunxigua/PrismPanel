<script setup>
import { computed, defineAsyncComponent, onBeforeUnmount, ref, watch } from "vue";
import { onBeforeRouteLeave } from "vue-router";
import {
  Download, Edit3, File, FileArchive, FileCode2, FilePlus2, FileText,
  Folder, FolderOpen, FolderPlus, MoreHorizontal, MoveRight, RefreshCw, Save,
  Trash2, Upload, X,
} from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { hasPermission } from "../../session";
import { downloadFile, fileJSON, importArchive, uploadFile } from "../../fileApi";

const CodeEditor = defineAsyncComponent(() => import("./CodeEditor.vue"));

const props = defineProps({
  nodeId: { type: String, required: true },
  server: { type: Object, required: true },
  instances: { type: Array, default: () => [] },
});

const treeRef = ref(null);
const treeKey = ref(0);
const treeLoading = ref(false);
const treeError = ref("");
const selectedTargetKey = ref("");
const activeDirectory = ref(".");
const selectedPath = ref("");
const uploadInput = ref(null);
const archiveInput = ref(null);
const uploadState = ref({ active: false, completed: 0, total: 0, name: "" });
const archiveImporting = ref(false);
const rootEmpty = ref(false);
const expandedPaths = ref([]);
const previewLoading = ref(false);
const previewSaving = ref(false);
const previewRequest = ref(0);
const preview = ref(emptyPreview());

const canWrite = computed(() => hasPermission("file.write"));
const canDelete = computed(() => hasPermission("file.delete"));
const targetOptions = computed(() => {
  const options = [];
  if (props.server?.type === "mirror") {
    options.push({ key: `image:${props.server.server_id}`, type: "image", id: props.server.server_id, label: "镜像源" });
  }
  for (const instance of props.instances) {
    options.push({
      key: `instance:${instance.instance_id}`,
      type: "instance",
      id: instance.instance_id,
      label: instance.name || instance.instance_id,
      instance,
    });
  }
  return options;
});
const currentTarget = computed(() => targetOptions.value.find((item) => item.key === selectedTargetKey.value));
const writeLocked = computed(() => {
  if (currentTarget.value?.type === "image") {
    return props.instances.some((item) => item.deployment_locked || item.state === "deploying");
  }
  return Boolean(currentTarget.value?.instance?.deployment_locked);
});
const writeDisabled = computed(() => !currentTarget.value || writeLocked.value);
const explorerBusy = computed(() => previewSaving.value || uploadState.value.active || archiveImporting.value);
const canImportArchive = computed(() => canWrite.value && rootEmpty.value && currentTarget.value && (
  currentTarget.value.type === "image"
  || ["stopped", "failed"].includes(currentTarget.value.instance?.state)
));
const previewDirty = computed(() => preview.value.kind === "text" && preview.value.content !== preview.value.original);
const previewName = computed(() => preview.value.name || preview.value.path.split("/").pop() || "文件");

watch(targetOptions, (options) => {
  if (!options.some((item) => item.key === selectedTargetKey.value)) {
    selectedTargetKey.value = options[0]?.key || "";
    resetExplorer();
  }
}, { immediate: true });

function authorization(scope, path, paths = [], extra = {}, target = currentTarget.value) {
  return {
    node_id: props.nodeId,
    scope,
    resource_type: target.type,
    resource_id: target.id,
    path,
    paths,
    ...extra,
  };
}

async function listPath(path, target) {
  const entries = [];
  let cursor = "";
  do {
    const result = await fileJSON(authorization("file.list", ".", [], {}, target), "POST", {
      directories: [{ path, cursor, limit: 500 }],
      include_hidden: true,
    });
    const directory = result.results?.[0];
    if (directory?.error) {
      const error = new Error(directory.error.message || "目录读取失败");
      error.code = directory.error.code;
      throw error;
    }
    entries.push(...(directory?.entries || []));
    cursor = directory?.truncated ? directory.next_cursor : "";
  } while (cursor);
  return entries;
}

async function loadTreeNode(node, resolve) {
  const target = currentTarget.value;
  if (!target) {
    resolve([]);
    return;
  }
  const directory = node.level === 0 ? "." : node.data.path;
  if (node.level > 0 && node.data.type !== "directory") {
    resolve([]);
    return;
  }
  if (node.level === 0) treeLoading.value = true;
  try {
    const entries = await listPath(directory, target);
    if (selectedTargetKey.value !== target.key) {
      resolve([]);
      return;
    }
    if (node.level === 0) rootEmpty.value = entries.length === 0;
    treeError.value = "";
    resolve(entries);
  } catch (error) {
    if (node.level === 0) {
      rootEmpty.value = false;
      treeError.value = error.message;
    }
    resolve([]);
  } finally {
    if (node.level === 0) treeLoading.value = false;
  }
}

async function changeTarget(key) {
  if (key === selectedTargetKey.value || explorerBusy.value) return;
  if (!await confirmDiscard()) return;
  selectedTargetKey.value = key;
  resetExplorer();
}

function resetExplorer() {
  previewRequest.value += 1;
  previewLoading.value = false;
  activeDirectory.value = ".";
  selectedPath.value = "";
  preview.value = emptyPreview();
  treeError.value = "";
  rootEmpty.value = false;
  expandedPaths.value = [];
  treeKey.value += 1;
}

function refreshTree() {
  treeError.value = "";
  rootEmpty.value = false;
  treeKey.value += 1;
}

function handleNodeExpand(data) {
  if (!expandedPaths.value.includes(data.path)) expandedPaths.value.push(data.path);
}

function handleNodeCollapse(data) {
  expandedPaths.value = expandedPaths.value.filter((path) => path !== data.path && !path.startsWith(`${data.path}/`));
}

async function handleNodeClick(data) {
  if (data.type === "directory") {
    activeDirectory.value = data.path;
    return;
  }
  await openFile(data);
}

async function openFile(entry) {
  if (entry.path === preview.value.path) return;
  if (!await confirmDiscard()) {
    treeRef.value?.setCurrentKey(preview.value.path || null);
    return;
  }
  selectedPath.value = entry.path;
  activeDirectory.value = parentPath(entry.path);
  previewLoading.value = true;
  const request = ++previewRequest.value;
  const target = currentTarget.value;
  preview.value = {
    ...emptyPreview(), path: entry.path, name: entry.name, size: entry.size,
    modified_at: entry.modified_at, kind: "loading",
  };
  try {
    const content = await fileJSON(authorization("file.read", entry.path, [], {}, target), "GET");
    if (request !== previewRequest.value || selectedTargetKey.value !== target.key) return;
    preview.value = { ...content, name: entry.name, original: content.content, kind: "text", error: "" };
  } catch (error) {
    if (request !== previewRequest.value || selectedTargetKey.value !== target.key) return;
    preview.value = {
      ...preview.value,
      kind: "unavailable",
      error: error.message,
      errorCode: error.code || "FILE_READ_FAILED",
    };
  } finally {
    if (request === previewRequest.value) previewLoading.value = false;
  }
}

async function closePreview() {
  if (!await confirmDiscard()) return;
  selectedPath.value = "";
  preview.value = emptyPreview();
  treeRef.value?.setCurrentKey(null);
}

async function savePreview() {
  if (!previewDirty.value || writeLocked.value) return;
  previewSaving.value = true;
  const target = currentTarget.value;
  const path = preview.value.path;
  try {
    const saved = await fileJSON(authorization("file.edit", path, [], {}, target), "PUT", {
      content: preview.value.content,
      encoding: preview.value.encoding,
      expected_version: preview.value.version,
    });
    if (selectedTargetKey.value !== target.key || preview.value.path !== path) return;
    preview.value = { ...saved, name: previewName.value, original: saved.content, kind: "text", error: "" };
    ElMessage.success("文件已保存");
  } catch (error) {
    if (error.code === "FILE_CHANGED") {
      try {
        await ElMessageBox.confirm("节点上的文件已经变化，重新加载会放弃当前修改。", "保存冲突", {
          type: "warning", confirmButtonText: "重新加载", cancelButtonText: "保留当前内容",
        });
        await reloadPreview();
      } catch { /* Keep local content. */ }
    } else {
      ElMessage.error(error.message);
    }
  } finally {
    previewSaving.value = false;
  }
}

async function reloadPreview() {
  if (!preview.value.path) return;
  const entry = { path: preview.value.path, name: previewName.value, size: preview.value.size, modified_at: preview.value.modified_at };
  preview.value = emptyPreview();
  await openFile(entry);
}

async function createEntry(type) {
  if (!canWrite.value || writeLocked.value) return;
  try {
    const { value } = await ElMessageBox.prompt(type === "directory" ? "目录名称" : "文件名称", type === "directory" ? "新建目录" : "新建文件", {
      confirmButtonText: "创建", cancelButtonText: "取消",
      inputValidator: (name) => validName(name) || "名称不能为空，且不能包含 / 或 \\。",
    });
    const targetPath = joinPath(activeDirectory.value, value.trim());
    await fileJSON(authorization("file.create", targetPath), "POST", { type });
    ElMessage.success(type === "directory" ? "目录已创建" : "文件已创建");
    refreshTree();
    if (type === "file") await openFile({ path: targetPath, name: value.trim(), size: 0 });
  } catch (error) {
    if (!isCancelled(error)) ElMessage.error(error.message || "创建失败");
  }
}

async function renameEntry(entry) {
  if (!canWrite.value || writeLocked.value) return;
  try {
    const { value } = await ElMessageBox.prompt("新名称", "重命名", {
      inputValue: entry.name, confirmButtonText: "重命名", cancelButtonText: "取消",
      inputValidator: (name) => validName(name) || "名称不能为空，且不能包含 / 或 \\。",
    });
    const destination = joinPath(parentPath(entry.path), value.trim());
    await moveEntry(entry, destination);
  } catch (error) {
    if (!isCancelled(error)) ElMessage.error(error.message || "重命名失败");
  }
}

async function promptMove(entry) {
  if (!canWrite.value || writeLocked.value) return;
  try {
    const { value } = await ElMessageBox.prompt("相对于工作目录的目标路径", "移动", {
      inputValue: entry.path, confirmButtonText: "移动", cancelButtonText: "取消",
      inputValidator: (path) => Boolean(path?.trim()) || "目标路径不能为空",
    });
    await moveEntry(entry, value.trim().replaceAll("\\", "/"));
  } catch (error) {
    if (!isCancelled(error)) ElMessage.error(error.message || "移动失败");
  }
}

async function moveEntry(entry, destination) {
  if (previewDirty.value && preview.value.path === entry.path && !await confirmDiscard()) return;
  await fileJSON(authorization("file.move", entry.path, [entry.path, destination]), "POST", {
    destination, overwrite: false,
  });
  if (preview.value.path === entry.path) preview.value = emptyPreview();
  selectedPath.value = "";
  ElMessage.success("文件已移动");
  refreshTree();
}

async function removeEntry(entry) {
  if (!canDelete.value || writeLocked.value) return;
  const recursive = entry.type === "directory";
  try {
    await ElMessageBox.confirm(
      recursive ? `将永久删除 ${entry.name} 及其全部内容。` : `将永久删除 ${entry.name}。`,
      "确认删除",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" },
    );
    if (preview.value.path === entry.path && previewDirty.value && !await confirmDiscard()) return;
    await fileJSON(authorization("file.delete", entry.path, [entry.path], { recursive }), "POST", {
      paths: [entry.path], recursive,
    });
    if (preview.value.path === entry.path || preview.value.path.startsWith(`${entry.path}/`)) {
      preview.value = emptyPreview();
      selectedPath.value = "";
    }
    ElMessage.success("删除完成");
    refreshTree();
  } catch (error) {
    if (!isCancelled(error)) ElMessage.error(error.message || "删除失败");
  }
}

function chooseFiles() {
  if (canWrite.value && !writeDisabled.value) uploadInput.value?.click();
}

function chooseArchive() {
  if (canWrite.value && !writeDisabled.value && rootEmpty.value) archiveInput.value?.click();
}

async function handleFileInput(event) {
  const files = Array.from(event.target.files || []);
  event.target.value = "";
  await uploadFiles(files);
}

async function handleArchiveInput(event) {
  const file = event.target.files?.[0];
  event.target.value = "";
  if (!file) return;
  if (!file.name.toLowerCase().endsWith(".zip")) {
    ElMessage.warning("仅支持 ZIP 压缩包");
    return;
  }
  archiveImporting.value = true;
  const target = currentTarget.value;
  try {
    const result = await importArchive(authorization("file.import", ".", [], {}, target), file);
    ElMessage.success(`已导入 ${result.files} 个文件`);
    refreshTree();
  } catch (error) {
    ElMessage.error(error.message || "压缩包导入失败");
  } finally {
    archiveImporting.value = false;
  }
}

async function handleDrop(event) {
  if (!canWrite.value || writeLocked.value) return;
  await uploadFiles(Array.from(event.dataTransfer?.files || []));
}

async function uploadFiles(files) {
  if (!files.length) return;
  const target = currentTarget.value;
  uploadState.value = { active: true, completed: 0, total: files.length, name: files[0].name };
  try {
    for (const file of files) {
      uploadState.value.name = file.name;
      const targetPath = joinPath(activeDirectory.value, file.name);
      try {
        await uploadFile(authorization("file.upload", targetPath, [], {}, target), file, false);
      } catch (error) {
        if (error.code !== "FILE_EXISTS") throw error;
        try {
          await ElMessageBox.confirm(`${file.name} 已存在，是否覆盖？`, "覆盖文件", {
            type: "warning", confirmButtonText: "覆盖", cancelButtonText: "跳过",
          });
        } catch (action) {
          if (isCancelled(action)) {
            uploadState.value.completed += 1;
            continue;
          }
          throw action;
        }
        await uploadFile(authorization("file.upload", targetPath, [], {}, target), file, true);
      }
      uploadState.value.completed += 1;
    }
    ElMessage.success("文件上传完成");
    refreshTree();
  } catch (error) {
    if (!isCancelled(error)) ElMessage.error(error.message || "文件上传失败");
  } finally {
    uploadState.value.active = false;
  }
}

async function downloadEntry(entry = preview.value) {
  if (!entry.path) return;
  try {
    await downloadFile(authorization("file.download", entry.path), entry.name || entry.path.split("/").pop());
  } catch (error) {
    if (error?.name !== "AbortError") ElMessage.error(error.message || "文件下载失败");
  }
}

async function confirmDiscard() {
  if (!previewDirty.value) return true;
  try {
    await ElMessageBox.confirm("当前文件有未保存修改。", "放弃修改", {
      type: "warning", confirmButtonText: "放弃", cancelButtonText: "继续编辑",
    });
    return true;
  } catch {
    return false;
  }
}

onBeforeRouteLeave(confirmDiscard);
function beforeUnload(event) {
  if (!previewDirty.value) return;
  event.preventDefault();
  event.returnValue = "";
}
window.addEventListener("beforeunload", beforeUnload);
onBeforeUnmount(() => window.removeEventListener("beforeunload", beforeUnload));

function emptyPreview() {
  return { path: "", name: "", content: "", original: "", encoding: "utf-8", version: "", size: 0, modified_at: "", kind: "empty", error: "", errorCode: "" };
}
function validName(value) {
  const name = value?.trim();
  return Boolean(name && name !== "." && name !== ".." && !name.includes("/") && !name.includes("\\"));
}
function isCancelled(value) { return value === "cancel" || value === "close"; }
function joinPath(parent, name) { return parent === "." ? name : `${parent}/${name}`; }
function parentPath(value) { const parts = value.split("/"); parts.pop(); return parts.join("/") || "."; }
function formatSize(value) {
  const size = Number(value) || 0;
  if (size < 1024) return `${size} B`;
  if (size < 1024 ** 2) return `${(size / 1024).toFixed(1)} KB`;
  if (size < 1024 ** 3) return `${(size / 1024 ** 2).toFixed(1)} MB`;
  return `${(size / 1024 ** 3).toFixed(2)} GB`;
}
function fileIcon(entry) {
  const extension = entry.name?.split(".").pop()?.toLowerCase();
  if (["zip", "tar", "gz", "rar", "7z"].includes(extension)) return FileArchive;
  if (["json", "yml", "yaml", "toml", "xml", "properties", "conf", "ini", "js", "ts", "java", "go", "sh", "bat"].includes(extension)) return FileCode2;
  if (["txt", "md", "log"].includes(extension)) return FileText;
  return File;
}
function fileIconClass(entry) {
  const extension = entry.name?.split(".").pop()?.toLowerCase();
  if (["zip", "tar", "gz", "rar", "7z"].includes(extension)) return "archive-icon";
  if (["json", "yml", "yaml", "toml", "xml", "properties", "conf", "ini", "js", "ts", "java", "go", "sh", "bat"].includes(extension)) return "code-icon";
  return "file-icon";
}
</script>

<template>
  <div class="file-manager" @dragover.prevent @drop.prevent="handleDrop">
    <aside class="explorer-pane">
      <div class="explorer-target">
        <el-select :model-value="selectedTargetKey" :disabled="explorerBusy" placeholder="选择文件目标" @change="changeTarget">
          <el-option v-for="item in targetOptions" :key="item.key" :label="item.label" :value="item.key" />
        </el-select>
        <el-tag v-if="writeLocked" type="warning" effect="plain">只读</el-tag>
      </div>

      <div class="explorer-heading">
        <strong>资源管理器</strong>
        <div class="explorer-actions">
          <el-tooltip content="新建文件"><button v-if="canWrite" type="button" :disabled="writeDisabled" @click="createEntry('file')"><FilePlus2 :size="15" /></button></el-tooltip>
          <el-tooltip content="新建目录"><button v-if="canWrite" type="button" :disabled="writeDisabled" @click="createEntry('directory')"><FolderPlus :size="15" /></button></el-tooltip>
          <el-tooltip content="上传文件"><button v-if="canWrite" type="button" :disabled="writeDisabled" @click="chooseFiles"><Upload :size="15" /></button></el-tooltip>
          <el-tooltip content="导入 ZIP"><button v-if="canImportArchive" type="button" :disabled="writeDisabled || archiveImporting" @click="chooseArchive"><FileArchive :size="15" /></button></el-tooltip>
          <el-tooltip content="刷新"><button type="button" @click="refreshTree"><RefreshCw :size="15" /></button></el-tooltip>
        </div>
      </div>

      <div class="explorer-location" :title="activeDirectory">
        <FolderOpen :size="13" /><span>{{ activeDirectory === "." ? "根目录" : activeDirectory }}</span>
      </div>

      <div v-if="treeError" class="explorer-error">{{ treeError }}</div>
      <el-tree
        v-else
        ref="treeRef"
        :key="`${selectedTargetKey}:${treeKey}`"
        v-loading="treeLoading"
        class="vscode-tree"
        lazy
        node-key="path"
        highlight-current
        :load="loadTreeNode"
        :props="{ label: 'name', children: 'children', isLeaf: (data) => data.type !== 'directory' }"
        :indent="14"
        :default-expanded-keys="expandedPaths"
        :current-node-key="selectedPath || undefined"
        :expand-on-click-node="true"
        @node-click="handleNodeClick"
        @node-expand="handleNodeExpand"
        @node-collapse="handleNodeCollapse"
      >
        <template #default="{ node, data }">
          <div class="explorer-node" :title="data.path">
            <FolderOpen v-if="data.type === 'directory' && node.expanded" class="folder-icon" :size="14" />
            <Folder v-else-if="data.type === 'directory'" class="folder-icon" :size="14" />
            <component :is="fileIcon(data)" v-else :class="fileIconClass(data)" :size="14" />
            <span>{{ data.name }}</span>
            <el-dropdown v-if="data.type === 'file' || canWrite || canDelete" trigger="click" class="node-menu" @click.stop>
              <button type="button" aria-label="文件操作" @click.stop><MoreHorizontal :size="15" /></button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item v-if="data.type === 'file'" @click="openFile(data)"><Edit3 :size="14" />打开</el-dropdown-item>
                  <el-dropdown-item v-if="data.type === 'file'" @click="downloadEntry(data)"><Download :size="14" />下载</el-dropdown-item>
                  <el-dropdown-item v-if="canWrite" :disabled="writeLocked" @click="renameEntry(data)"><Edit3 :size="14" />重命名</el-dropdown-item>
                  <el-dropdown-item v-if="canWrite" :disabled="writeLocked" @click="promptMove(data)"><MoveRight :size="14" />移动</el-dropdown-item>
                  <el-dropdown-item v-if="canDelete" :disabled="writeLocked" divided @click="removeEntry(data)"><Trash2 :size="14" /><span class="danger">删除</span></el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
        </template>
      </el-tree>

      <div v-if="uploadState.active" class="upload-status">
        <div><span>{{ uploadState.name }}</span><strong>{{ uploadState.completed }}/{{ uploadState.total }}</strong></div>
        <el-progress :percentage="Math.round(uploadState.completed / uploadState.total * 100)" :stroke-width="3" :show-text="false" />
      </div>
      <input ref="uploadInput" type="file" multiple hidden @change="handleFileInput" />
      <input ref="archiveInput" type="file" accept=".zip,application/zip" hidden @change="handleArchiveInput" />
    </aside>

    <section class="editor-pane">
      <template v-if="preview.path">
        <div class="editor-tabbar">
          <div class="editor-tab active">
            <component :is="fileIcon(preview)" :size="14" />
            <span>{{ previewName }}</span>
            <i v-if="previewDirty" />
            <button type="button" aria-label="关闭文件" @click="closePreview"><X :size="14" /></button>
          </div>
          <div class="editor-commands">
            <el-tooltip content="下载"><button type="button" @click="downloadEntry()"><Download :size="15" /></button></el-tooltip>
            <el-tooltip v-if="canWrite && preview.kind === 'text'" content="保存"><button type="button" :disabled="!previewDirty || writeLocked || previewSaving" @click="savePreview"><Save :size="15" /></button></el-tooltip>
          </div>
        </div>
        <div class="editor-breadcrumb" :title="preview.path">
          <span v-for="(part, index) in preview.path.split('/')" :key="`${part}:${index}`"><template v-if="index">/</template>{{ part }}</span>
        </div>
        <div v-loading="previewLoading" class="editor-content">
          <CodeEditor
            v-if="preview.kind === 'text'"
            v-model="preview.content"
            :disabled="writeLocked || !canWrite"
            :file-path="preview.path"
            @save="savePreview"
          />
          <div v-else-if="preview.kind === 'unavailable'" class="preview-unavailable">
            <FileArchive v-if="preview.errorCode === 'UNSUPPORTED_ENCODING'" :size="38" />
            <File v-else :size="38" />
            <strong>{{ previewName }}</strong>
            <span>{{ preview.error }}</span>
            <small>{{ formatSize(preview.size) }}</small>
            <el-button @click="downloadEntry()"><Download :size="15" />下载文件</el-button>
          </div>
        </div>
        <footer class="editor-statusbar">
          <span>{{ preview.encoding?.toUpperCase() || "-" }}</span>
          <span>{{ formatSize(preview.size) }}</span>
          <span v-if="writeLocked">部署锁定</span>
          <span v-else-if="previewDirty">已修改</span>
          <span v-else>已同步</span>
        </footer>
      </template>
      <div v-else class="editor-empty">
        <FileCode2 :size="42" />
        <strong>未打开文件</strong>
      </div>
    </section>
  </div>
</template>

<style scoped>
.file-manager {
  display: grid;
  grid-template-columns: minmax(240px, 300px) minmax(0, 1fr);
  height: clamp(560px, calc(100vh - 220px), 820px);
  min-height: 560px;
  overflow: hidden;
  border: 1px solid #cfd7d1;
  background: #fff;
}
.explorer-pane { display: flex; min-width: 0; flex-direction: column; overflow: hidden; border-right: 1px solid #cfd7d1; background: #f6f8f6; }
.explorer-target { display: flex; min-height: 44px; align-items: center; gap: 6px; border-bottom: 1px solid #dce2dd; padding: 6px 8px; }
.explorer-target .el-select { min-width: 0; flex: 1; }
.explorer-heading { display: flex; min-height: 34px; align-items: center; justify-content: space-between; padding: 0 7px 0 12px; }
.explorer-heading strong { color: #3d4841; font-size: 11px; font-weight: 700; text-transform: uppercase; }
.explorer-actions, .editor-commands { display: flex; align-items: center; gap: 2px; }
.explorer-actions button, .editor-commands button, .node-menu button, .editor-tab button {
  display: grid; width: 26px; height: 26px; place-items: center; border: 0; padding: 0; background: transparent; color: #58645c; cursor: pointer;
}
.explorer-actions button:hover, .editor-commands button:hover, .node-menu button:hover, .editor-tab button:hover { background: #e2e7e3; color: #27332b; }
.explorer-actions button:disabled, .editor-commands button:disabled { cursor: not-allowed; opacity: 0.38; }
.explorer-location { display: flex; min-height: 27px; align-items: center; gap: 6px; border-block: 1px solid #e3e7e4; padding: 0 10px; color: #727e75; font-size: 11px; }
.explorer-location span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.explorer-error { margin: 8px; border-left: 3px solid #bd4b43; padding: 8px; color: #8b3934; font-size: 12px; }
.vscode-tree { min-height: 0; flex: 1; overflow: auto; background: transparent; --el-tree-node-hover-bg-color: #e7ebe8; }
.vscode-tree :deep(.el-tree-node__content) { height: 24px; padding-right: 3px; }
.vscode-tree :deep(.el-tree-node.is-current > .el-tree-node__content) { background: #d9e5de; }
.vscode-tree :deep(.el-tree-node__expand-icon) { padding: 4px 2px; font-size: 10px; }
.explorer-node { display: flex; min-width: 0; height: 24px; flex: 1; align-items: center; gap: 5px; }
.explorer-node > svg { flex: 0 0 auto; }
.explorer-node > .folder-icon { color: #b78930; }
.explorer-node > .archive-icon { color: #9a6b32; }
.explorer-node > .code-icon { color: #4774a8; }
.explorer-node > .file-icon { color: #69736d; }
.explorer-node > span { min-width: 0; flex: 1; overflow: hidden; color: #313b35; font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.node-menu { flex: 0 0 auto; opacity: 0; }
.node-menu button { width: 22px; height: 22px; }
.explorer-node:hover .node-menu { opacity: 1; }
.upload-status { border-top: 1px solid #dce2dd; padding: 7px 9px; background: #fff; }
.upload-status > div { display: flex; justify-content: space-between; gap: 8px; margin-bottom: 4px; color: #647067; font-size: 11px; }
.upload-status span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.editor-pane { display: flex; min-width: 0; min-height: 0; flex-direction: column; overflow: hidden; background: #fff; }
.editor-tabbar { display: flex; min-height: 36px; align-items: stretch; justify-content: space-between; border-bottom: 1px solid #d8ded9; background: #f1f4f2; }
.editor-tab { display: flex; min-width: 140px; max-width: 280px; align-items: center; gap: 6px; border-right: 1px solid #d8ded9; padding-left: 10px; background: #fff; color: #344038; font-size: 12px; }
.editor-tab > span { min-width: 0; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.editor-tab > i { width: 7px; height: 7px; flex: 0 0 auto; border-radius: 50%; background: #b77723; }
.editor-commands { padding: 0 7px; }
.editor-breadcrumb { min-height: 29px; overflow: hidden; border-bottom: 1px solid #e2e6e3; padding: 6px 11px; color: #747f77; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.editor-breadcrumb span { margin-right: 4px; }
.editor-content { min-height: 0; flex: 1; overflow: hidden; }
.editor-content :deep(.code-editor), .editor-content :deep(.cm-editor), .editor-content :deep(.cm-scroller) { height: 100%; min-height: 0; }
.preview-unavailable, .editor-empty { display: grid; height: 100%; place-items: center; align-content: center; gap: 9px; color: #7b867e; text-align: center; }
.preview-unavailable strong, .editor-empty strong { color: #3d4841; font-size: 14px; }
.preview-unavailable span, .editor-empty span { max-width: 440px; font-size: 12px; }
.preview-unavailable small { color: #929b95; }
.editor-statusbar { display: flex; min-height: 24px; align-items: center; justify-content: flex-end; gap: 14px; border-top: 1px solid #d8ded9; padding: 0 9px; background: #f5f7f5; color: #67736b; font-size: 10px; }
.danger { color: #b83c35; }
@media (max-width: 780px) {
  .file-manager { grid-template-columns: 1fr; grid-template-rows: 250px minmax(420px, 1fr); height: auto; min-height: 700px; }
  .explorer-pane { border-right: 0; border-bottom: 1px solid #cfd7d1; }
  .editor-pane { min-height: 420px; }
}
</style>
