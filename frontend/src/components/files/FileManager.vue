<script setup>
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { onBeforeRouteLeave } from "vue-router";
import {
  ArrowUp, ChevronRight, ClipboardPaste, Copy, Download, Edit3, File, FileArchive, FileCode2,
  FilePlus2, FileText, Folder, FolderOpen, FolderPlus, Home, MoreHorizontal, MoveRight,
  RefreshCw, Save, Scissors, Trash2, Upload, X,
} from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { hasPermission } from "../../session";
import { downloadFile, fileExportURL, fileJSON, importArchive, uploadFileWithProgress } from "../../fileApi";
import { formatBytes } from "../../formatBytes";
import { isExternalFileDrag, plainUploadItems, scanDroppedItems } from "../../fileDrop";
import { isWinApp, openRemoteFileWinApp, runtimeConfig } from "../../runtime";
import { showUploadResult } from "../../uploadResult";
import UploadConflictDialog from "./UploadConflictDialog.vue";

const CodeEditor = defineAsyncComponent(() => import("./CodeEditor.vue"));

const props = defineProps({
  nodeId: { type: String, required: true },
  server: { type: Object, required: true },
  instances: { type: Array, default: () => [] },
});

const directoryEntries = ref([]);
const directoryLoading = ref(false);
const directoryError = ref("");
const directoryRequest = ref(0);
const selectedTargetKey = ref("");
const activeDirectory = ref(".");
const pathInput = ref(".");
const uploadInput = ref(null);
const archiveInput = ref(null);
const conflictDialog = ref(null);
const dragTargetPath = ref("");
const uploadState = ref({
  active: false, completed: 0, total: 0, name: "", scanning: false,
  directories: 0, uploaded: 0, overwritten: 0, skipped: 0, failed: 0,
  failures: [],
  progress: { loaded: 0, total: 0, percent: 0 },
  transferred: 0,
  cancelled: false,
});
const downloadState = ref({ active: false, name: "", loaded: 0, total: 0, percent: 0, rate: 0 });
let activeUploadCancel = null;
const archiveImporting = ref(false);
const archiveCreating = ref(false);
const rootEmpty = ref(false);
const previewLoading = ref(false);
const previewSaving = ref(false);
const previewRequest = ref(0);
const preview = ref(emptyPreview());
const contextMenu = ref({ visible: false, x: 0, y: 0, entry: null });
const tabs = ref([]);
const activeTabKey = ref("");
const selectedEntries = ref([]);
const pathEditing = ref(false);
const clipboard = ref({ type: "", entries: [], targetKey: "" });
const MAX_TABS = 10;
let longPressTimer = 0;
let longPressOrigin = null;
let suppressEntryClick = false;
let stopFileSyncEvents = null;

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
const explorerBusy = computed(() => previewSaving.value || uploadState.value.active || archiveImporting.value || archiveCreating.value);
const canImportArchive = computed(() => canWrite.value && rootEmpty.value && currentTarget.value && (
  currentTarget.value.type === "image"
  || ["stopped", "failed"].includes(currentTarget.value.instance?.state)
));
const previewDirty = computed(() => preview.value.kind === "text" && preview.value.content !== preview.value.original);
const previewName = computed(() => preview.value.name || preview.value.path.split("/").pop() || "文件");
const pathSegments = computed(() => {
  const segments = [{ path: ".", name: "根目录" }];
  if (activeDirectory.value === ".") return segments;
  let current = "";
  for (const part of activeDirectory.value.split("/").filter(Boolean)) {
    current = current ? `${current}/${part}` : part;
    segments.push({ path: current, name: part });
  }
  return segments;
});
const hasClipboard = computed(() => clipboard.value.entries.length > 0);

watch(targetOptions, (options) => {
  if (!options.some((item) => item.key === selectedTargetKey.value)) {
    selectedTargetKey.value = options[0]?.key || "";
    resetExplorer();
  }
}, { immediate: true });

watch(selectedTargetKey, () => {
  if (selectedTargetKey.value) restoreTabs();
});

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

async function loadDirectory(path = activeDirectory.value, target = currentTarget.value) {
  if (!target) {
    directoryEntries.value = [];
    return false;
  }
  const request = ++directoryRequest.value;
  directoryLoading.value = true;
  directoryError.value = "";
  try {
    const entries = await listPath(path, target);
    if (request !== directoryRequest.value || selectedTargetKey.value !== target.key) return false;
    activeDirectory.value = path;
    pathInput.value = path;
    updateActiveTab(path, target);
    directoryEntries.value = entries;
    if (path === ".") rootEmpty.value = entries.length === 0;
    return true;
  } catch (error) {
    if (request !== directoryRequest.value) return false;
    directoryError.value = error.message;
    if (path === ".") rootEmpty.value = false;
    return false;
  } finally {
    if (request === directoryRequest.value) directoryLoading.value = false;
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
  directoryRequest.value += 1;
  previewLoading.value = false;
  activeDirectory.value = ".";
  pathInput.value = ".";
  selectedEntries.value = [];
  pathEditing.value = false;
  directoryEntries.value = [];
  preview.value = emptyPreview();
  directoryError.value = "";
  rootEmpty.value = false;
  closeContextMenu();
  restoreTabs();
}

function refreshDirectory() {
  void loadDirectory(activeDirectory.value);
}

async function navigateDirectory(path) {
  if (path === activeDirectory.value && !preview.value.path) return refreshDirectory();
  if (!await confirmDiscard()) return;
  preview.value = emptyPreview();
  await loadDirectory(path);
}

function submitPath() {
  const normalized = pathInput.value.trim().replaceAll("\\", "/").replace(/^\/+|\/+$/g, "") || ".";
  pathEditing.value = false;
  void navigateDirectory(normalized);
}

function goParentDirectory() {
  if (activeDirectory.value !== ".") void navigateDirectory(parentPath(activeDirectory.value));
}

function openEntry(entry) {
  if (suppressEntryClick) return;
  closeContextMenu();
  if (entry.type === "directory") void navigateDirectory(entry.path);
  else if (isWinApp()) void openNativeFile(entry);
  else void openFile(entry);
}

async function openNativeFile(entry, chooseApplication = false) {
  try {
    await openRemoteFileWinApp({
      node_id: props.nodeId,
      resource_type: currentTarget.value.type,
      resource_id: currentTarget.value.id,
      path: entry.path,
      name: entry.name,
      size: Number(entry.size) || 0,
    }, chooseApplication);
    ElMessage.success("文件已在本机打开，保存后将自动回传");
  } catch (error) {
    ElMessage.error(error.message || "本机打开失败");
  }
}

function selectEntry(entry, event) {
  if (event?.metaKey || event?.ctrlKey) {
    const index = selectedEntries.value.findIndex((item) => item.path === entry.path);
    if (index >= 0) selectedEntries.value.splice(index, 1);
    else selectedEntries.value.push(entry);
  } else {
    selectedEntries.value = [entry];
  }
}

function isSelected(entry) {
  return selectedEntries.value.some((item) => item.path === entry.path);
}

function targetStorageKey(target = currentTarget.value) {
  if (!target) return "";
  const panel = runtimeConfig.panelUrl || runtimeConfig.apiBaseUrl || window.location.origin;
  return `prism:file-path:v2:${encodeURIComponent(panel)}:${props.nodeId}:${target.key}`;
}

function restoreTabs() {
  const target = currentTarget.value;
  if (!target) return;
  let stored;
  try { stored = JSON.parse(window.localStorage.getItem(targetStorageKey(target)) || "null"); } catch { stored = null; }
  const path = normalizePath(stored?.lastPath || ".");
  const tab = { key: createTabKey(), path, name: tabName(path), closable: false };
  tabs.value = [tab];
  activeTabKey.value = tab.key;
  activeDirectory.value = path;
  pathInput.value = path;
  saveTabs(target);
  void loadDirectory(path, target);
}

function saveTabs(target = currentTarget.value) {
  const key = targetStorageKey(target);
  if (!key) return;
  window.localStorage.setItem(key, JSON.stringify({ lastPath: activeDirectory.value }));
}

function updateActiveTab(path, target = currentTarget.value) {
  const active = tabs.value.find((item) => item.key === activeTabKey.value);
  if (active) {
    active.path = path;
    active.name = tabName(path);
  }
  saveTabs(target);
}

async function switchTab(key) {
  if (key === activeTabKey.value) return true;
  if (explorerBusy.value || !await confirmDiscard()) return false;
  const tab = tabs.value.find((item) => item.key === key);
  if (!tab) return false;
  activeTabKey.value = key;
  selectedEntries.value = [];
  preview.value = emptyPreview();
  saveTabs();
  return loadDirectory(tab.path);
}

async function addTab(path = ".") {
  if (tabs.value.length >= MAX_TABS) {
    ElMessage.warning(`最多打开 ${MAX_TABS} 个标签`);
    return;
  }
  if (explorerBusy.value || !await confirmDiscard()) return;
  const tab = { key: createTabKey(), path: normalizePath(path), name: tabName(path), closable: true };
  tabs.value.push(tab);
  activeTabKey.value = tab.key;
  selectedEntries.value = [];
  preview.value = emptyPreview();
  saveTabs();
  await loadDirectory(tab.path);
}

async function closeTab(key) {
  if (tabs.value.length <= 1) return;
  if (!await confirmDiscard()) return;
  const index = tabs.value.findIndex((item) => item.key === key);
  if (index < 0) return;
  tabs.value.splice(index, 1);
  if (activeTabKey.value === key) {
    activeTabKey.value = tabs.value[index - 1]?.key || tabs.value[0].key;
    const tab = tabs.value.find((item) => item.key === activeTabKey.value);
    preview.value = emptyPreview();
    await loadDirectory(tab.path);
  }
  saveTabs();
}

function openInNewTab(entry) {
  void addTab(entry.type === "directory" ? entry.path : parentPath(entry.path));
}

function createTabKey() { return `tab-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`; }
function normalizePath(value) { return value?.trim().replaceAll("\\", "/").replace(/^\/+|\/+$/g, "") || "."; }
function tabName(value) { return value === "." ? "根目录" : value.split("/").pop() || "根目录"; }

async function openFile(entry) {
  if (entry.path === preview.value.path) return;
  if (!await confirmDiscard()) {
    return;
  }
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
  preview.value = emptyPreview();
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
    refreshDirectory();
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
  ElMessage.success("文件已移动");
  selectedEntries.value = [];
  refreshDirectory();
}

function setClipboard(type, entry = null) {
  if (!canWrite.value || writeLocked.value) return;
  const entries = entry ? [entry] : selectedEntries.value;
  if (!entries.length) {
    ElMessage.warning("请先选择文件或文件夹");
    return;
  }
  clipboard.value = {
    type,
    targetKey: selectedTargetKey.value,
    entries: entries.map((item) => ({ path: item.path, name: item.name, type: item.type })),
  };
  ElMessage.success(type === "copy" ? "已复制到剪贴板" : "已剪切到剪贴板");
}

async function pasteClipboard() {
  if (!hasClipboard.value || !canWrite.value || writeLocked.value) return;
  if (clipboard.value.targetKey !== selectedTargetKey.value) {
    ElMessage.warning("只能在同一个文件目标内粘贴");
    return;
  }
  const pending = [...clipboard.value.entries];
  const failures = [];
  for (const item of pending) {
    const destination = joinPath(activeDirectory.value, item.name);
    try {
      const scope = clipboard.value.type === "copy" ? "file.copy" : "file.move";
      await fileJSON(authorization(scope, item.path, [item.path, destination]), "POST", {
        destination, overwrite: false,
      });
    } catch (error) {
      failures.push(`${item.name}: ${error.message || "失败"}`);
    }
  }
  if (failures.length) ElMessage.warning(`粘贴完成，${failures.length} 项失败`);
  else ElMessage.success("粘贴完成");
  if (clipboard.value.type === "move") clipboard.value = { type: "", entries: [], targetKey: "" };
  selectedEntries.value = [];
  refreshDirectory();
}

async function archiveEntry(entry) {
  if (!canWrite.value || writeLocked.value || archiveCreating.value) return;
  try {
    const defaultName = entry.name.toLowerCase().endsWith(".zip")
      ? entry.name.replace(/\.zip$/i, "-archive.zip")
      : entry.name + ".zip";
    const { value } = await ElMessageBox.prompt("压缩包名称", "压缩为 ZIP", {
      inputValue: defaultName,
      confirmButtonText: "压缩",
      cancelButtonText: "取消",
      inputValidator: (name) => validName(name) && name.toLowerCase().endsWith(".zip")
        ? true
        : "请输入有效的 .zip 文件名",
    });
    const destination = joinPath(parentPath(entry.path), value.trim());
    archiveCreating.value = true;
    await fileJSON(
      authorization("file.archive", entry.path, [entry.path, destination]),
      "POST",
      { destination },
    );
    ElMessage.success("压缩完成");
    refreshDirectory();
  } catch (error) {
    if (!isCancelled(error)) ElMessage.error(error.message || "压缩失败");
  } finally {
    archiveCreating.value = false;
  }
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
    }
    ElMessage.success("删除完成");
    refreshDirectory();
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
  await uploadItems(plainUploadItems(files), activeDirectory.value);
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
    refreshDirectory();
  } catch (error) {
    ElMessage.error(error.message || "压缩包导入失败");
  } finally {
    archiveImporting.value = false;
  }
}

function handleDragOver(event, directory = activeDirectory.value) {
  if (!isExternalFileDrag(event) || !canWrite.value || writeDisabled.value) return;
  event.preventDefault();
  event.dataTransfer.dropEffect = "copy";
  dragTargetPath.value = directory;
}

function handleDragLeave(event) {
  if (!event.currentTarget.contains(event.relatedTarget)) dragTargetPath.value = "";
}

async function handleDrop(event, directory = activeDirectory.value) {
  if (!isExternalFileDrag(event) || !canWrite.value || writeDisabled.value) return;
  event.preventDefault();
  event.stopPropagation();
  dragTargetPath.value = "";
  uploadState.value = {
    active: true, completed: 0, total: 0, name: "正在扫描拖入内容", scanning: true,
    directories: 0, uploaded: 0, overwritten: 0, skipped: 0, failed: 0,
    progress: { loaded: 0, total: 0, percent: 0 },
    transferred: 0,
    cancelled: false,
  };
  try {
    const scanned = await scanDroppedItems(event.dataTransfer);
    if (uploadState.value.cancelled) {
      uploadState.value.active = false;
      return;
    }
    await uploadItems(scanned, directory, true);
  } catch (error) {
    ElMessage.error(error.message || "拖入内容读取失败");
    uploadState.value.active = false;
  }
}

async function uploadItems(items, baseDirectory) {
  if (!items.files.length && !items.directories.length) {
    uploadState.value.active = false;
    return;
  }
  const target = currentTarget.value;
  uploadState.value = {
    active: true, completed: 0, total: items.files.length, name: items.files[0]?.path || "创建目录",
    scanning: false, directories: 0, uploaded: 0, overwritten: 0, skipped: 0, failed: 0,
    failures: [],
    progress: { loaded: 0, total: 0, percent: 0 },
    transferred: 0,
    cancelled: false,
  };
  let overwriteAll = false;
  let completedBytes = 0;
  try {
    for (const directory of items.directories) {
      if (uploadState.value.cancelled) break;
      const targetPath = joinPath(baseDirectory, directory);
      try {
        await fileJSON(authorization("file.create", targetPath, [], {}, target), "POST", { type: "directory" });
        uploadState.value.directories += 1;
      } catch (error) {
        uploadState.value.failed += 1;
        uploadState.value.failures.push({ name: directory, error: error.message || "创建目录失败" });
      }
    }
    for (const item of items.files) {
      if (uploadState.value.cancelled) break;
      uploadState.value.name = item.path;
      uploadState.value.progress = { loaded: 0, total: item.file.size || 0, percent: 0 };
      const targetPath = joinPath(baseDirectory, item.path);
      const result = await uploadOneFile(target, targetPath, item.file, overwriteAll, (event) => {
        uploadState.value.progress = {
          loaded: event.loaded,
          total: event.total,
          percent: event.total ? Math.round((event.loaded / event.total) * 100) : 0,
        };
        uploadState.value.transferred = completedBytes + event.loaded;
      });
      if (uploadState.value.cancelled) break;
      if (result.status === "overwrite-all") overwriteAll = true;
      if (result.status === "uploaded") uploadState.value.uploaded += 1;
      if (result.status === "overwritten" || result.status === "overwrite-all") uploadState.value.overwritten += 1;
      if (result.status === "skipped") uploadState.value.skipped += 1;
      if (result.status === "failed") {
        uploadState.value.failed += 1;
        uploadState.value.failures.push({ name: item.path, error: result.error || "上传失败" });
      }
      if (result.status === "uploaded" || result.status === "overwritten" || result.status === "overwrite-all") {
        completedBytes += item.file.size || 0;
      }
      uploadState.value.transferred = completedBytes;
      uploadState.value.completed += 1;
    }
    if (uploadState.value.cancelled) {
      // 取消提示、刷新已由 cancelUpload 处理（已上传文件保留在服务器）。
      return;
    }
    showUploadSummary(uploadState.value);
    refreshDirectory();
  } catch (error) {
    if (!isCancelled(error)) ElMessage.error(error.message || "文件上传失败");
  } finally {
    uploadState.value.active = false;
    activeUploadCancel = null;
  }
}

async function uploadOneFile(target, targetPath, file, overwriteAll, onProgress) {
  if (uploadState.value.cancelled) return { status: "cancelled" };
  try {
    const handle = uploadFileWithProgress(authorization("file.upload", targetPath, [], {}, target), file, overwriteAll, onProgress);
    activeUploadCancel = typeof handle.cancel === "function" ? handle.cancel : null;
    try {
      await handle;
      return { status: overwriteAll ? "overwritten" : "uploaded" };
    } finally {
      activeUploadCancel = null;
    }
  } catch (error) {
    if (error.name === "AbortError") return { status: "cancelled" };
    if (error.code !== "FILE_EXISTS") {
      if (["RESULT_UNKNOWN", "UNAUTHENTICATED"].includes(error.code)) throw error;
      return { status: "failed", error: error.message || "上传失败" };
    }
  }
  const action = await conflictDialog.value.ask({
    title: "目标文件已存在",
    message: "目标位置已存在「" + targetPath + "」，请选择处理方式。",
    detail: "“全部覆盖”仅对本次拖入或选择的后续重复文件生效。",
    allowOverwriteAll: true,
  });
  if (action === "skip") return { status: "skipped" };
  if (uploadState.value.cancelled) return { status: "cancelled" };
  try {
    const handle = uploadFileWithProgress(authorization("file.upload", targetPath, [], {}, target), file, true, onProgress);
    activeUploadCancel = typeof handle.cancel === "function" ? handle.cancel : null;
    try {
      await handle;
      return { status: action === "overwrite-all" ? "overwrite-all" : "overwritten" };
    } finally {
      activeUploadCancel = null;
    }
  } catch (error) {
    if (error.name === "AbortError") return { status: "cancelled" };
    if (["RESULT_UNKNOWN", "UNAUTHENTICATED"].includes(error.code)) throw error;
    return { status: "failed", error: error.message || "上传失败" };
  }
}

function cancelUpload() {
  if (!uploadState.value.active || uploadState.value.cancelled) return;
  uploadState.value.cancelled = true;
  if (typeof activeUploadCancel === "function") activeUploadCancel();
  const kept = (uploadState.value.uploaded || 0) + (uploadState.value.overwritten || 0);
  uploadState.value.active = false;
  uploadState.value.scanning = false;
  refreshDirectory();
  ElMessage.info(`已取消上传（已上传 ${kept} 个文件保留）`);
}

function showUploadSummary(state) {
  const parts = [];
  if (state.directories) parts.push(`已创建 ${state.directories} 个目录`);
  if (state.uploaded) parts.push(`已上传 ${state.uploaded} 个文件`);
  if (state.overwritten) parts.push(`已覆盖 ${state.overwritten} 个文件`);
  if (state.skipped) parts.push(`跳过 ${state.skipped} 个文件`);
  showUploadResult(ElMessage, {
    parts,
    successEmpty: "未上传文件",
    failed: state.failed || 0,
    failures: state.failures || [],
    succeeded: (state.directories || 0) + (state.uploaded || 0) + (state.overwritten || 0),
    noun: "文件",
  });
}

function dropDirectory(entry) {
  return entry.type === "directory" ? entry.path : parentPath(entry.path);
}

function handleEntryDragStart(event, entry) {
  if (event.target.closest?.("button")) {
    event.preventDefault();
    return;
  }
  const target = currentTarget.value;
  if (!target || !entry.path || !event.dataTransfer) return;
  const name = entry.type === "directory" ? entry.name + ".zip" : entry.name;
  const safeName = name.replaceAll(":", "_").replaceAll("\r", "").replaceAll("\n", "");
  const mime = entry.type === "directory" ? "application/zip" : "application/octet-stream";
  const url = fileExportURL(authorization("file.download", entry.path, [], {}, target));
  event.dataTransfer.effectAllowed = "copy";
  event.dataTransfer.setData("DownloadURL", mime + ":" + safeName + ":" + url);
  event.dataTransfer.setData("text/uri-list", url);
  event.dataTransfer.setData("text/plain", safeName);
  event.dataTransfer.setData("application/x-prism-file-entry", entry.path);
}

function showContextMenu(event, entry) {
  event.preventDefault();
  event.stopPropagation();
  if (!isSelected(entry)) selectEntry(entry, event);
  const menuWidth = 196;
  const menuHeight = entry.type === "directory" ? 278 : 238;
  contextMenu.value = {
    visible: true,
    x: Math.max(8, Math.min(event.clientX, window.innerWidth - menuWidth - 8)),
    y: Math.max(8, Math.min(event.clientY, window.innerHeight - menuHeight - 8)),
    entry,
  };
}

function showEntryMenu(event, entry) {
  const bounds = event.currentTarget.getBoundingClientRect();
  showContextMenu({
    clientX: bounds.right,
    clientY: bounds.bottom,
    preventDefault() {},
    stopPropagation() {},
  }, entry);
}

function closeContextMenu() {
  contextMenu.value = { visible: false, x: 0, y: 0, entry: null };
}

function beginLongPress(event, entry) {
  if (event.pointerType === "mouse") return;
  cancelLongPress();
  const point = { x: event.clientX, y: event.clientY };
  longPressOrigin = point;
  longPressTimer = window.setTimeout(() => {
    suppressEntryClick = true;
    showContextMenu({
      clientX: point.x,
      clientY: point.y,
      preventDefault() {},
      stopPropagation() {},
    }, entry);
    if (navigator.vibrate) navigator.vibrate(20);
  }, 520);
}

function moveLongPress(event) {
  if (!longPressOrigin) return;
  if (Math.hypot(event.clientX - longPressOrigin.x, event.clientY - longPressOrigin.y) > 8) cancelLongPress();
}

function cancelLongPress() {
  window.clearTimeout(longPressTimer);
  longPressTimer = 0;
  longPressOrigin = null;
}

function handleDocumentClick(event) {
  if (suppressEntryClick) {
    suppressEntryClick = false;
    return;
  }
  if (!event.target.closest?.(".file-context-menu")) closeContextMenu();
}

function runContextAction(action) {
  const entry = contextMenu.value.entry;
  suppressEntryClick = false;
  closeContextMenu();
  if (!entry) return;
  if (action === "open") openEntry(entry);
  if (action === "online-edit") void openFile(entry);
  if (action === "open-with") void openNativeFile(entry, true);
  if (action === "new-tab") openInNewTab(entry);
  if (action === "download") void downloadEntry(entry);
  if (action === "copy") setClipboard("copy", entry);
  if (action === "cut") setClipboard("move", entry);
  if (action === "paste") void pasteClipboard();
  if (action === "archive") void archiveEntry(entry);
  if (action === "rename") void renameEntry(entry);
  if (action === "move") void promptMove(entry);
  if (action === "delete") void removeEntry(entry);
}

async function downloadEntry(entry = preview.value) {
  if (!entry.path) return;
  const name = entry.name || entry.path.split("/").pop();
  const fileName = entry.type === "directory" ? name + ".zip" : name;
  downloadState.value = { active: true, name: fileName, loaded: 0, total: 0, percent: 0, rate: 0 };
  try {
    await downloadFile(authorization("file.download", entry.path), fileName, (event) => {
      downloadState.value = {
        active: true, name: fileName,
        loaded: event.loaded, total: event.total, percent: event.percent, rate: event.rate || 0,
      };
    });
  } catch (error) {
    if (error?.name !== "AbortError") ElMessage.error(error.message || "文件下载失败");
  } finally {
    downloadState.value.active = false;
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
onMounted(() => {
  document.addEventListener("click", handleDocumentClick);
  window.addEventListener("resize", closeContextMenu);
  window.addEventListener("blur", closeContextMenu);
  stopFileSyncEvents = window.runtime?.EventsOn?.("prism:file-sync", handleFileSyncEvent) || null;
});
onBeforeUnmount(() => {
  cancelLongPress();
  window.removeEventListener("beforeunload", beforeUnload);
  document.removeEventListener("click", handleDocumentClick);
  window.removeEventListener("resize", closeContextMenu);
  window.removeEventListener("blur", closeContextMenu);
  if (typeof stopFileSyncEvents === "function") stopFileSyncEvents();
  else window.runtime?.EventsOff?.("prism:file-sync");
  stopFileSyncEvents = null;
});

function handleFileSyncEvent(event) {
  if (event?.type === "synced") ElMessage.success(`已自动回传 ${event.path}`);
  if (event?.type === "updated") ElMessage.info(`云端文件已更新本地缓存：${event.path}`);
  if (event?.type === "error") ElMessage.error(event.message || "文件自动回传失败");
}

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
function formatDate(value) {
  if (!value) return "-";
  return new Date(value).toLocaleString("zh-CN", { hour12: false });
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
  <div
    class="file-manager"
    :class="{ 'drop-active': dragTargetPath }"
    @dragover="handleDragOver"
    @dragleave="handleDragLeave"
    @drop="handleDrop"
  >
    <aside v-show="!preview.path" class="explorer-pane">
      <div class="explorer-target">
        <el-select :model-value="selectedTargetKey" :disabled="explorerBusy" placeholder="选择文件目标" @change="changeTarget">
          <el-option v-for="item in targetOptions" :key="item.key" :label="item.label" :value="item.key" />
        </el-select>
        <el-tag v-if="writeLocked" type="warning" effect="plain">只读</el-tag>
      </div>

      <div class="explorer-heading">
        <strong>资源管理器</strong>
        <div class="explorer-actions">
          <el-tooltip content="新建标签"><button type="button" :disabled="explorerBusy || tabs.length >= MAX_TABS" @click="addTab()"><FilePlus2 :size="15" /></button></el-tooltip>
          <el-tooltip content="新建文件"><button v-if="canWrite" type="button" :disabled="writeDisabled" @click="createEntry('file')"><FilePlus2 :size="15" /></button></el-tooltip>
          <el-tooltip content="新建目录"><button v-if="canWrite" type="button" :disabled="writeDisabled" @click="createEntry('directory')"><FolderPlus :size="15" /></button></el-tooltip>
          <el-tooltip content="上传文件"><button v-if="canWrite" type="button" :disabled="writeDisabled" @click="chooseFiles"><Upload :size="15" /></button></el-tooltip>
          <el-tooltip content="导入 ZIP"><button v-if="canImportArchive" type="button" :disabled="writeDisabled || archiveImporting" @click="chooseArchive"><FileArchive :size="15" /></button></el-tooltip>
          <el-tooltip content="粘贴"><button v-if="hasClipboard" type="button" :disabled="writeDisabled" @click="pasteClipboard"><ClipboardPaste :size="15" /></button></el-tooltip>
          <el-tooltip content="复制所选"><button v-if="selectedEntries.length" type="button" :disabled="writeDisabled" @click="setClipboard('copy')"><Copy :size="15" /></button></el-tooltip>
          <el-tooltip content="剪切所选"><button v-if="selectedEntries.length" type="button" :disabled="writeDisabled" @click="setClipboard('move')"><Scissors :size="15" /></button></el-tooltip>
          <el-tooltip content="刷新"><button type="button" @click="refreshDirectory"><RefreshCw :size="15" /></button></el-tooltip>
        </div>
      </div>

      <div class="file-tabs" role="tablist">
        <button
          v-for="tab in tabs"
          :key="tab.key"
          type="button"
          class="file-tab"
          :class="{ active: tab.key === activeTabKey }"
          role="tab"
          :aria-selected="tab.key === activeTabKey"
          @click="switchTab(tab.key)"
        >
          <Folder :size="13" />
          <span :title="tab.path">{{ tab.name }}</span>
          <X v-if="tab.closable" :size="13" @click.stop="closeTab(tab.key)" />
        </button>
        <button class="file-tab-add" type="button" aria-label="新建标签" :disabled="tabs.length >= MAX_TABS" @click="addTab()">+</button>
      </div>

      <form class="explorer-location" @submit.prevent="submitPath">
        <button
          class="location-up"
          type="button"
          aria-label="返回上一级"
          :disabled="activeDirectory === '.'"
          @click="goParentDirectory"
        >
          <ArrowUp :size="15" />
        </button>
        <Home :size="14" />
        <div v-if="!pathEditing" class="location-breadcrumbs" @click="pathEditing = true">
          <button
            v-for="(segment, index) in pathSegments"
            :key="segment.path"
            type="button"
            class="location-segment"
            :title="segment.path"
            @click.stop="navigateDirectory(segment.path)"
          >
            <template v-if="index">/</template>{{ segment.name }}
          </button>
        </div>
        <input v-else v-model="pathInput" aria-label="当前路径" spellcheck="false" @blur="submitPath" />
        <button v-if="pathEditing" class="location-go" type="submit" aria-label="打开路径"><ChevronRight :size="15" /></button>
        <span v-else class="location-edit-hint" aria-hidden="true">点击编辑</span>
      </form>

      <div v-if="directoryError" class="explorer-error">
        <span>{{ directoryError }}</span>
        <el-button text size="small" @click="refreshDirectory">重试</el-button>
      </div>
      <div v-else v-loading="directoryLoading" class="file-list-shell">
        <div class="file-list-head" aria-hidden="true">
          <span>名称</span><span>大小</span><span>修改时间</span><span />
        </div>
        <div v-if="!directoryLoading && directoryEntries.length === 0" class="file-list-empty">
          <FolderOpen :size="34" />
          <span>此目录为空</span>
        </div>
        <div v-else class="file-list" role="list">
          <div
            v-for="entry in directoryEntries"
            :key="entry.path"
            class="file-list-row"
            :class="{ 'drop-target': dragTargetPath === dropDirectory(entry), selected: isSelected(entry) }"
            :title="entry.path"
            role="listitem"
            tabindex="0"
            draggable="true"
            @click="selectEntry(entry, $event)"
            @dblclick="openEntry(entry)"
            @keydown.enter.prevent="openEntry(entry)"
            @contextmenu="showContextMenu($event, entry)"
            @pointerdown="beginLongPress($event, entry)"
            @pointermove="moveLongPress"
            @pointerup="cancelLongPress"
            @pointercancel="cancelLongPress"
            @dragstart.stop="handleEntryDragStart($event, entry)"
            @dragover.stop="handleDragOver($event, dropDirectory(entry))"
            @dragleave.stop="handleDragLeave"
            @drop.stop="handleDrop($event, dropDirectory(entry))"
          >
            <span class="file-list-name">
              <Folder v-if="entry.type === 'directory'" class="folder-icon" :size="18" />
              <component :is="fileIcon(entry)" v-else :class="fileIconClass(entry)" :size="18" />
              <span><strong>{{ entry.name }}</strong><small>{{ entry.type === 'directory' ? '文件夹' : formatSize(entry.size) }} · {{ formatDate(entry.modified_at) }}</small></span>
            </span>
            <span class="file-list-size">{{ entry.type === 'directory' ? '-' : formatSize(entry.size) }}</span>
            <time>{{ formatDate(entry.modified_at) }}</time>
            <button class="file-row-menu" type="button" aria-label="文件操作" @click.stop="showEntryMenu($event, entry)">
              <MoreHorizontal :size="17" />
            </button>
          </div>
        </div>
      </div>

      <div v-if="uploadState.active" class="upload-status">
        <div>
          <span>{{ uploadState.name }}</span>
          <strong>{{ uploadState.scanning ? "扫描中" : uploadState.completed + "/" + uploadState.total + " 个文件" }}</strong>
          <button v-if="!uploadState.cancelled" type="button" class="upload-cancel" @click="cancelUpload">取消</button>
        </div>
        <div v-if="!uploadState.scanning && uploadState.progress.total" class="upload-status-detail">
          <span>上传中 {{ uploadState.progress.percent }}%（{{ formatBytes(uploadState.progress.loaded) }} / {{ formatBytes(uploadState.progress.total) }}）</span>
          <span>已传 {{ formatBytes(uploadState.transferred) }}</span>
        </div>
        <el-progress
          :percentage="uploadState.scanning ? 0 : (uploadState.progress.total ? uploadState.progress.percent : (uploadState.total ? Math.round(uploadState.completed / uploadState.total * 100) : 0))"
          :indeterminate="uploadState.scanning"
          :duration="1.2"
          :stroke-width="3"
          :show-text="false"
        />
      </div>
      <div v-if="downloadState.active" class="upload-status">
        <div>
          <span>下载 {{ downloadState.name }}</span>
          <strong>{{ downloadState.total ? downloadState.percent + "%" : formatBytes(downloadState.loaded) }}</strong>
        </div>
        <div v-if="downloadState.total" class="upload-status-detail">
          <span>已下载 {{ formatBytes(downloadState.loaded) }} / {{ formatBytes(downloadState.total) }}</span>
          <span v-if="downloadState.rate">速率 {{ formatBytes(downloadState.rate) }}/s</span>
        </div>
        <el-progress
          :percentage="downloadState.total ? downloadState.percent : 0"
          :indeterminate="!downloadState.total && downloadState.loaded === 0"
          :duration="1.2"
          :stroke-width="3"
          :show-text="false"
        />
      </div>
      <input ref="uploadInput" type="file" multiple hidden @change="handleFileInput" />
      <input ref="archiveInput" type="file" accept=".zip,application/zip" hidden @change="handleArchiveInput" />
    </aside>

    <section v-if="preview.path" class="editor-pane">
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
    </section>
    <UploadConflictDialog ref="conflictDialog" />
    <Teleport to="body">
      <div
        v-if="contextMenu.visible && contextMenu.entry"
        class="file-context-menu"
        :style="{ left: contextMenu.x + 'px', top: contextMenu.y + 'px' }"
        role="menu"
        @click.stop
        @contextmenu.prevent
      >
        <button type="button" role="menuitem" @click="runContextAction('open')">
          <FolderOpen v-if="contextMenu.entry.type === 'directory'" :size="16" />
          <Edit3 v-else :size="16" />
          {{ contextMenu.entry.type === "directory" ? "打开文件夹" : "打开文件" }}
        </button>
        <button v-if="isWinApp() && contextMenu.entry.type === 'file'" type="button" role="menuitem" @click="runContextAction('open-with')"><FolderOpen :size="16" />选择打开方式</button>
        <button v-if="isWinApp() && contextMenu.entry.type === 'file'" type="button" role="menuitem" @click="runContextAction('online-edit')"><Edit3 :size="16" />在线编辑</button>
        <button v-if="contextMenu.entry.type === 'directory'" type="button" role="menuitem" @click="runContextAction('new-tab')"><Folder :size="16" />在新标签中打开</button>
        <button type="button" role="menuitem" @click="runContextAction('download')"><Download :size="16" />下载</button>
        <div class="file-context-separator" />
        <button v-if="canWrite" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('copy')"><Copy :size="16" />复制</button>
        <button v-if="canWrite" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('cut')"><Scissors :size="16" />剪切</button>
        <button v-if="hasClipboard" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('paste')"><ClipboardPaste :size="16" />粘贴</button>
        <div class="file-context-separator" />
        <button v-if="canWrite" type="button" role="menuitem" :disabled="writeLocked || archiveCreating" @click="runContextAction('archive')"><FileArchive :size="16" />压缩为 ZIP</button>
        <button v-if="canWrite" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('rename')"><Edit3 :size="16" />重命名</button>
        <button v-if="canWrite" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('move')"><MoveRight :size="16" />移动到</button>
        <template v-if="canDelete">
          <div class="file-context-separator" />
          <button class="danger" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('delete')"><Trash2 :size="16" />删除</button>
        </template>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.file-manager {
  --file-accent: #5e8c70;
  --file-accent-text: #245c45;
  --file-selected-bg: #dcebe1;
  --file-drop-bg: #dce9e1;
  --file-danger: #b83c35;
  --file-danger-text: #8b3934;
  --file-danger-bg: #fff5f3;
  --file-danger-border: #bd4b43;
  display: flex;
  flex-direction: column;
  height: clamp(560px, calc(100vh - 220px), 820px);
  min-height: 560px;
  overflow: hidden;
  border: 1px solid var(--app-border);
  color: var(--app-text-secondary);
  background: var(--app-surface);
}
:global(html.dark) .file-manager {
  --file-accent: #76b58d;
  --file-accent-text: #8dcba3;
  --file-selected-bg: #294032;
  --file-drop-bg: #294032;
  --file-danger: #efa49b;
  --file-danger-text: #efa49b;
  --file-danger-bg: #3d2421;
  --file-danger-border: #b65f55;
}
.file-manager.drop-active { border-color: var(--file-accent); }
.explorer-pane { display: flex; min-width: 0; min-height: 0; flex: 1; flex-direction: column; overflow: hidden; background: var(--app-surface); }
.explorer-target { display: flex; min-height: 48px; align-items: center; gap: 8px; border-bottom: 1px solid var(--app-border); padding: 7px 10px; background: var(--app-surface-muted); }
.explorer-target .el-select { min-width: 0; flex: 1; }
.explorer-heading { display: flex; min-height: 42px; align-items: center; justify-content: space-between; gap: 10px; padding: 5px 8px 5px 12px; }
.explorer-heading strong { color: var(--app-text); font-size: 12px; font-weight: 700; }
.explorer-actions, .editor-commands { display: flex; align-items: center; gap: 2px; }
.explorer-actions button, .editor-commands button, .editor-tab button {
  display: grid; width: 30px; height: 30px; place-items: center; border: 1px solid transparent; border-radius: 4px; padding: 0; background: transparent; color: var(--app-text-secondary); cursor: pointer;
}
.explorer-actions button:hover, .editor-commands button:hover, .editor-tab button:hover { border-color: var(--app-border); background: var(--app-surface-hover); color: var(--app-text); }
.explorer-actions button:disabled, .editor-commands button:disabled { cursor: not-allowed; opacity: 0.38; }
.file-tabs { display: flex; min-height: 36px; align-items: stretch; overflow-x: auto; border-bottom: 1px solid var(--app-border); background: var(--app-surface-muted); }
.file-tab { display: flex; min-width: 120px; max-width: 220px; align-items: center; gap: 6px; border: 0; border-right: 1px solid var(--app-border); padding: 0 9px; color: var(--app-text-muted); background: transparent; cursor: pointer; font-size: 11px; }
.file-tab:hover { background: var(--app-surface-hover); }
.file-tab.active { color: var(--app-text); background: var(--app-surface); box-shadow: inset 0 2px 0 var(--file-accent); }
.file-tab > span { min-width: 0; flex: 1; overflow: hidden; text-align: left; text-overflow: ellipsis; white-space: nowrap; }
.file-tab > svg:last-child { flex: 0 0 auto; opacity: .7; }
.file-tab-add { width: 34px; flex: 0 0 auto; border: 0; color: var(--app-text-muted); background: transparent; cursor: pointer; font-size: 19px; }
.file-tab-add:hover:not(:disabled) { background: var(--app-surface-hover); }
.file-tab-add:disabled { cursor: not-allowed; opacity: .35; }
.explorer-location { display: grid; min-height: 38px; grid-template-columns: 28px 16px minmax(0, 1fr) auto; align-items: center; gap: 5px; border-block: 1px solid var(--app-border-soft); padding: 4px 8px; color: var(--app-text-muted); background: var(--app-surface-muted); }
.location-up, .location-go { display: grid; width: 28px; height: 28px; place-items: center; border: 0; border-radius: 4px; padding: 0; color: inherit; background: transparent; }
.explorer-location button:hover:not(:disabled) { color: var(--file-accent-text); background: var(--app-surface-hover); }
.explorer-location button:disabled { cursor: default; opacity: 0.35; }
.explorer-location input { width: 100%; min-width: 0; height: 28px; border: 1px solid transparent; border-radius: 4px; padding: 0 7px; color: var(--app-text); background: transparent; font: 11px Consolas, monospace; }
.explorer-location input:hover, .explorer-location input:focus { border-color: var(--app-border); background: var(--app-surface); outline: 0; }
.location-breadcrumbs { display: flex; min-width: 0; align-items: center; overflow: hidden; }
.location-segment { display: block; width: auto; min-width: 0; max-width: min(240px, 40vw); height: 28px; flex: 0 1 auto; overflow: hidden; border: 0; border-radius: 4px; padding: 0 5px; color: var(--app-text-secondary); background: transparent; cursor: pointer; font: 11px/28px Consolas, monospace; text-overflow: ellipsis; white-space: nowrap; }
.location-segment:hover { color: var(--file-accent-text); background: var(--app-surface-hover); }
.location-edit-hint { padding-right: 4px; color: var(--app-text-subtle); font-size: 10px; white-space: nowrap; }
.explorer-error { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin: 10px; border-left: 3px solid var(--file-danger-border); padding: 9px 10px; color: var(--file-danger-text); background: var(--file-danger-bg); font-size: 12px; }
.file-list-shell { position: relative; min-height: 0; flex: 1; overflow: hidden; }
.file-list-head, .file-list-row { display: grid; grid-template-columns: minmax(240px, 1fr) 110px 170px 38px; align-items: center; gap: 12px; }
.file-list-head { min-height: 34px; padding: 0 9px 0 14px; color: var(--app-text-muted); background: var(--app-surface-muted); border-bottom: 1px solid var(--app-border-soft); font-size: 10px; font-weight: 700; }
.file-list { height: calc(100% - 34px); overflow: auto; overscroll-behavior: contain; }
.file-list-row { min-height: 48px; padding: 4px 9px 4px 14px; border-bottom: 1px solid var(--app-border-soft); color: var(--app-text-secondary); cursor: default; user-select: none; }
.file-list-row:hover, .file-list-row:focus-visible { background: var(--app-surface-hover); outline: 0; }
.file-list-row.selected { background: var(--file-selected-bg); box-shadow: inset 3px 0 var(--file-accent); }
.file-list-row.drop-target { background: var(--file-drop-bg); box-shadow: inset 0 0 0 1px var(--file-accent); }
.file-list-row.selected:hover, .file-list-row.selected:focus-visible { background: var(--file-selected-bg); }
.file-list-row.drop-target:hover, .file-list-row.drop-target:focus-visible { background: var(--file-drop-bg); }
.file-list-name { display: flex; min-width: 0; align-items: center; gap: 9px; }
.file-list-name > svg { flex: 0 0 auto; }
.file-list-name > span { min-width: 0; }
.file-list-name strong, .file-list-name small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-list-name strong { color: var(--app-text); font-size: 12px; font-weight: 600; }
.file-list-name small { display: none; margin-top: 3px; color: var(--app-text-muted); font-size: 9px; }
.folder-icon { color: #b78930; }
.archive-icon { color: #9a6b32; }
.code-icon { color: #4774a8; }
.file-icon { color: #69736d; }
.file-list-size, .file-list-row time { color: var(--app-text-muted); font-size: 11px; }
.file-row-menu { display: grid; width: 30px; height: 30px; place-items: center; border: 1px solid transparent; border-radius: 4px; padding: 0; color: var(--app-text-muted); background: transparent; }
.file-row-menu:hover { border-color: var(--app-border); color: var(--app-text); background: var(--app-surface-hover); }
.file-list-empty { display: grid; min-height: 220px; place-items: center; align-content: center; gap: 9px; color: var(--app-text-muted); font-size: 12px; }
.upload-status { border-top: 1px solid var(--app-border); padding: 7px 9px; background: var(--app-surface); }
.upload-status > div { display: flex; justify-content: space-between; gap: 8px; margin-bottom: 4px; color: var(--app-text-muted); font-size: 11px; }
.upload-status span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.upload-status-detail { font-size: 10px; }
.upload-cancel { flex: 0 0 auto; border: 0; padding: 0 2px; color: var(--file-danger-text, #a23f36); background: transparent; cursor: pointer; font-size: 11px; }
.upload-cancel:hover { text-decoration: underline; }
.editor-pane { display: flex; min-width: 0; min-height: 0; flex: 1; flex-direction: column; overflow: hidden; background: var(--app-surface); }
.editor-tabbar { display: flex; min-height: 36px; align-items: stretch; justify-content: space-between; border-bottom: 1px solid var(--app-border); background: var(--app-surface-muted); }
.editor-tab { display: flex; min-width: 140px; max-width: 280px; align-items: center; gap: 6px; border-right: 1px solid var(--app-border); padding-left: 10px; background: var(--app-surface); color: var(--app-text-secondary); font-size: 12px; }
.editor-tab > span { min-width: 0; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.editor-tab > i { width: 7px; height: 7px; flex: 0 0 auto; border-radius: 50%; background: #b77723; }
.editor-commands { padding: 0 7px; }
.editor-breadcrumb { min-height: 29px; overflow: hidden; border-bottom: 1px solid var(--app-border-soft); padding: 6px 11px; color: var(--app-text-muted); font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.editor-breadcrumb span { margin-right: 4px; }
.editor-content { min-height: 0; flex: 1; overflow: hidden; }
.editor-content :deep(.code-editor), .editor-content :deep(.cm-editor), .editor-content :deep(.cm-scroller) { height: 100%; min-height: 0; }
.preview-unavailable, .editor-empty { display: grid; height: 100%; place-items: center; align-content: center; gap: 9px; color: var(--app-text-muted); text-align: center; }
.preview-unavailable strong, .editor-empty strong { color: var(--app-text); font-size: 14px; }
.preview-unavailable span, .editor-empty span { max-width: 440px; font-size: 12px; }
.preview-unavailable small { color: var(--app-text-subtle); }
.editor-statusbar { display: flex; min-height: 24px; align-items: center; justify-content: flex-end; gap: 14px; border-top: 1px solid var(--app-border); padding: 0 9px; background: var(--app-surface-muted); color: var(--app-text-muted); font-size: 10px; }
.file-context-menu { --file-menu-danger: #a23f36; position: fixed; z-index: 5000; display: grid; width: 196px; padding: 5px; color: var(--app-text); background: var(--app-surface); border: 1px solid var(--app-border); border-radius: 6px; box-shadow: var(--app-shadow); }
:global(html.dark) .file-context-menu { --file-menu-danger: #efa49b; }
.file-context-menu button { display: flex; width: 100%; min-height: 34px; align-items: center; gap: 9px; border: 0; border-radius: 4px; padding: 6px 9px; color: inherit; background: transparent; text-align: left; font-size: 12px; }
.file-context-menu button:hover:not(:disabled) { color: var(--app-text); background: var(--app-surface-hover); }
.file-context-menu button:disabled { cursor: not-allowed; opacity: 0.42; }
.file-context-menu button.danger { color: var(--file-menu-danger); }
.file-context-separator { height: 1px; margin: 4px 5px; background: var(--app-border-soft); }
.danger { color: var(--file-danger); }
@media (max-width: 780px) {
  .file-manager { height: min(72vh, 680px); min-height: 480px; }
  .explorer-target { flex-wrap: wrap; }
  .explorer-heading { align-items: flex-start; }
  .explorer-actions { flex-wrap: wrap; justify-content: flex-end; }
  .file-list-head { display: none; }
  .file-list { height: 100%; -webkit-overflow-scrolling: touch; }
  .file-list-row { grid-template-columns: minmax(0, 1fr) 36px; min-height: 58px; gap: 6px; padding: 6px 8px 6px 11px; touch-action: pan-y; }
  .file-list-name small { display: block; }
  .file-list-size, .file-list-row > time { display: none; }
  .file-row-menu { width: 36px; height: 36px; }
  .editor-pane { min-height: 0; }
  .editor-tab { min-width: 0; max-width: calc(100vw - 110px); }
  .editor-statusbar { gap: 9px; }
}
</style>
