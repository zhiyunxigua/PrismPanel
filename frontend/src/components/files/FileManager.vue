<script setup>
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { onBeforeRouteLeave } from "vue-router";
import {
  ArrowUp, ChevronRight, ClipboardPaste, Copy, Download, Edit3, File, FileArchive, FileCode2,
  FilePlus2, FileText, Folder, FolderOpen, FolderPlus, Home, MoreHorizontal, MoveRight,
  RefreshCw, RotateCcw, Save, Scissors, Trash2, Upload, X,
} from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { hasPermission } from "../../session";
import {
  cancelUpload, clearRecycleBin, createUploadID, deleteRecycleEntries, downloadFile, fileExportURL, fileJSON,
  importArchive, listRecycleBin, restoreRecycleEntry, uploadFile,
} from "../../fileApi";
import { isExternalFileDrag, plainUploadItems, scanDroppedItems } from "../../fileDrop";
import { invertFileSelection, selectFileEntry } from "../../fileSelection";
import { isWinApp, openRemoteFileWinApp, runtimeConfig } from "../../runtime";
import UploadConflictDialog from "./UploadConflictDialog.vue";
import ExtractDialog from "./ExtractDialog.vue";
import UploadTaskDialog from "./UploadTaskDialog.vue";

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
const uploadDirectoryInput = ref(null);
const archiveInput = ref(null);
const conflictDialog = ref(null);
const dragTargetPath = ref("");
const uploadTasks = ref([]);
const uploadDialogVisible = ref(false);
const uploadScanning = ref(false);
const uploadPreparing = ref("");
const uploadWorkerActive = ref(false);
const uploadEmptyDirectories = ref([]);
const uploadStats = ref({ totalBytes: 0, loadedBytes: 0, completed: 0, startedAt: 0, speed: 0 });
const uploadSummary = ref({ directories: 0, uploaded: 0, overwritten: 0, skipped: 0, failed: 0 });
const extractDialogVisible = ref(false);
const extractEntry = ref(null);
const extractTask = ref(null);
const archiveImporting = ref(false);
const archiveCreating = ref(false);
const recycleDialogVisible = ref(false);
const recycleLoading = ref(false);
const recycleEntries = ref([]);
const rootEmpty = ref(false);
const previewLoading = ref(false);
const previewSaving = ref(false);
const previewRequest = ref(0);
const preview = ref(emptyPreview());
const contextMenu = ref({ visible: false, x: 0, y: 0, entry: null });
const contextMenuElement = ref(null);
const selectedEntries = ref([]);
const lastSelectedIndex = ref(-1);
const pathEditing = ref(false);
const clipboard = ref({ type: "", entries: [], targetKey: "" });
let longPressTimer = 0;
let longPressOrigin = null;
let suppressEntryClick = false;
let stopFileSyncEvents = null;

const canWrite = computed(() => hasPermission("file.write") || Boolean(currentTarget.value?.instance?.instance_admin));
const canDelete = computed(() => hasPermission("file.delete") || Boolean(currentTarget.value?.instance?.instance_admin));
const targetOptions = computed(() => {
  const options = [];
  if (props.server?.type === "mirror" && hasPermission("file.read")) {
    options.push({ key: `image:${props.server.server_id}`, type: "image", id: props.server.server_id, label: "镜像源" });
  }
  for (const instance of props.instances) {
    if (!hasPermission("file.read") && !instance.instance_admin) continue;
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
const explorerBusy = computed(() => (
  previewSaving.value || archiveImporting.value || archiveCreating.value
));
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
const multiContextSelection = computed(() => (
  selectedEntries.value.length > 1
  && Boolean(contextMenu.value.entry)
  && isSelected(contextMenu.value.entry)
));
const allSelected = computed(() => directoryEntries.value.length > 0 && selectedEntries.value.length === directoryEntries.value.length);
const selectionIndeterminate = computed(() => selectedEntries.value.length > 0 && !allSelected.value);
const activeUploadCount = computed(() => uploadTasks.value.filter((item) => ["waiting", "uploading", "paused"].includes(item.status)).length);
const currentUploadTaskId = computed(() => uploadTasks.value.find((item) => item.status === "uploading")?.id || "");

watch(targetOptions, (options) => {
  if (!options.some((item) => item.key === selectedTargetKey.value)) {
    selectedTargetKey.value = options[0]?.key || "";
    resetExplorer();
  }
}, { immediate: true });

watch(selectedTargetKey, () => {
  if (selectedTargetKey.value) restoreDirectory();
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
    saveDirectory(target);
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
  restoreDirectory();
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
  const result = selectFileEntry(directoryEntries.value, selectedEntries.value, entry, event, lastSelectedIndex.value);
  selectedEntries.value = result.selected;
  lastSelectedIndex.value = result.lastIndex;
}

function toggleEntry(entry, checked) {
  if (checked && !isSelected(entry)) selectedEntries.value.push(entry);
  if (!checked) selectedEntries.value = selectedEntries.value.filter((item) => item.path !== entry.path);
  lastSelectedIndex.value = directoryEntries.value.findIndex((item) => item.path === entry.path);
}

function toggleAll(checked) {
  selectedEntries.value = checked ? [...directoryEntries.value] : [];
  lastSelectedIndex.value = checked && directoryEntries.value.length ? directoryEntries.value.length - 1 : -1;
}

function invertSelection() {
  selectedEntries.value = invertFileSelection(directoryEntries.value, selectedEntries.value);
  lastSelectedIndex.value = -1;
}

function clearSelection(event) {
  if (event?.target !== event?.currentTarget) return;
  selectedEntries.value = [];
  lastSelectedIndex.value = -1;
}

function isSelected(entry) {
  return selectedEntries.value.some((item) => item.path === entry.path);
}

function targetStorageKey(target = currentTarget.value) {
  if (!target) return "";
  const panel = runtimeConfig.panelUrl || runtimeConfig.apiBaseUrl || window.location.origin;
  return `prism:file-path:v2:${encodeURIComponent(panel)}:${props.nodeId}:${target.key}`;
}

function restoreDirectory() {
  const target = currentTarget.value;
  if (!target) return;
  let stored;
  try { stored = JSON.parse(window.localStorage.getItem(targetStorageKey(target)) || "null"); } catch { stored = null; }
  const path = normalizePath(stored?.lastPath || ".");
  activeDirectory.value = path;
  pathInput.value = path;
  saveDirectory(target);
  void loadDirectory(path, target);
}

function saveDirectory(target = currentTarget.value) {
  const key = targetStorageKey(target);
  if (!key) return;
  window.localStorage.setItem(key, JSON.stringify({ lastPath: activeDirectory.value }));
}
function normalizePath(value) { return value?.trim().replaceAll("\\", "/").replace(/^\/+|\/+$/g, "") || "."; }

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

function openExtractDialog(entry) {
  if (!canWrite.value || writeLocked.value || entry.type !== "file" || !entry.name.toLowerCase().endsWith(".zip")) return;
  extractEntry.value = entry;
  extractTask.value = null;
  extractDialogVisible.value = true;
}

async function startExtract({ destination, password, encoding, directoryMode, conflictPolicy }) {
  const entry = extractEntry.value;
  const target = currentTarget.value;
  if (!entry || !target) return;
  try {
    extractTask.value = await fileJSON(
      authorization("file.extract", entry.path, [entry.path, destination], {}, target),
      "POST",
      {
        destination, password, encoding,
        directory_mode: directoryMode,
        conflict_policy: conflictPolicy,
      },
    );
    await pollExtractTask(target, entry.path, extractTask.value.id);
  } catch (error) {
    extractTask.value = {
      ...(extractTask.value || {}), status: "failed", stage: error.stage || "start",
      message: error.message || "解压失败", error: serializeFileError(error),
    };
  }
}

async function pollExtractTask(target, source, taskId) {
  while (extractDialogVisible.value && extractTask.value?.id === taskId) {
    await delay(500);
    const task = await fileJSON(
      authorization("file.extract.status", source, [], {}, target),
      "POST",
      { task_id: taskId },
    );
    extractTask.value = task;
    if (task.status === "done") {
      ElMessage.success("解压完成");
      refreshDirectory();
      return;
    }
    if (task.status === "failed") return;
  }
}

function delay(milliseconds) {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
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

async function removeSelectedEntries() {
  if (!canDelete.value || writeLocked.value || !selectedEntries.value.length) return;
  const entries = [...selectedEntries.value];
  const paths = entries.map((item) => item.path);
  const recursive = entries.some((item) => item.type === "directory");
  try {
    await ElMessageBox.confirm(`将永久删除所选 ${entries.length} 项${recursive ? "及目录内的全部内容" : ""}。`, "批量删除", {
      type: "warning", confirmButtonText: "删除", cancelButtonText: "取消",
    });
    for (let index = 0; index < paths.length; index += 100) {
      const batch = paths.slice(index, index + 100);
      await fileJSON(authorization("file.delete", batch[0], batch, { recursive }), "POST", { paths: batch, recursive });
    }
    selectedEntries.value = [];
    lastSelectedIndex.value = -1;
    ElMessage.success("删除完成");
    refreshDirectory();
  } catch (error) {
    if (!isCancelled(error)) ElMessage.error(error.message || "删除失败");
  }
}

async function openRecycleBin() {
  if (!currentTarget.value) return;
  recycleDialogVisible.value = true;
  await loadRecycleBin();
}

async function loadRecycleBin() {
  if (!currentTarget.value) return;
  recycleLoading.value = true;
  try {
    const result = await listRecycleBin(authorization("file.recycle.list", "."));
    recycleEntries.value = result.entries || [];
  } catch (error) {
    ElMessage.error(error.message || "回收站读取失败");
  } finally {
    recycleLoading.value = false;
  }
}

async function restoreRecycle(item) {
  try {
    await ElMessageBox.confirm(`将恢复到原路径「${item.original_path}」。`, "恢复文件", {
      type: "warning", confirmButtonText: "恢复", cancelButtonText: "取消",
    });
    await restoreRecycleEntry(authorization("file.recycle.restore", "."), item.id);
    ElMessage.success("恢复完成");
    await loadRecycleBin();
    refreshDirectory();
  } catch (error) {
    if (!isCancelled(error)) ElMessage.error(error.message || "恢复失败");
  }
}

async function permanentlyDeleteRecycle(item) {
  try {
    await ElMessageBox.confirm(`将永久删除「${item.name}」，此操作不可恢复。`, "彻底删除", {
      type: "warning", confirmButtonText: "彻底删除", cancelButtonText: "取消",
    });
    await deleteRecycleEntries(authorization("file.recycle.delete", "."), [item.id]);
    ElMessage.success("已彻底删除");
    await loadRecycleBin();
  } catch (error) {
    if (!isCancelled(error)) ElMessage.error(error.message || "彻底删除失败");
  }
}

async function emptyRecycleBin() {
  if (!recycleEntries.value.length) return;
  try {
    await ElMessageBox.confirm("回收站中的所有文件将被永久删除，此操作不可恢复。", "清空回收站", {
      type: "warning", confirmButtonText: "清空", cancelButtonText: "取消",
    });
    await clearRecycleBin(authorization("file.recycle.clear", "."));
    recycleEntries.value = [];
    ElMessage.success("回收站已清空");
  } catch (error) {
    if (!isCancelled(error)) ElMessage.error(error.message || "清空回收站失败");
  }
}

function chooseFiles() {
  if (canWrite.value && !writeDisabled.value) {
    uploadDialogVisible.value = true;
    uploadInput.value?.click();
  }
}

function chooseDirectory() {
  if (canWrite.value && !writeDisabled.value) {
    uploadDialogVisible.value = true;
    uploadDirectoryInput.value?.click();
  }
}

function chooseArchive() {
  if (canWrite.value && !writeDisabled.value && rootEmpty.value) archiveInput.value?.click();
}

async function handleFileInput(event) {
  const files = Array.from(event.target.files || []);
  event.target.value = "";
  await uploadItems(plainUploadItems(files), activeDirectory.value);
}

async function handleDirectoryInput(event) {
  const files = Array.from(event.target.files || []);
  event.target.value = "";
  if (!files.length) return;
  uploadDialogVisible.value = true;
  uploadScanning.value = true;
  await renderUploadDialog();
  try {
    await uploadItems(plainUploadItems(files), activeDirectory.value);
  } catch (error) {
    ElMessage.error(error.message || "文件夹内容读取失败");
  } finally {
    uploadScanning.value = false;
  }
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
  uploadScanning.value = true;
  uploadDialogVisible.value = true;
  const droppedFiles = Array.from(event.dataTransfer?.files || []);
  const scanPromise = isWinApp() ? null : scanDroppedItems(event.dataTransfer);
  try {
    await renderUploadDialog();
    if (isWinApp()) {
      if (!droppedFiles.length) {
        ElMessage.warning("当前客户端暂不支持拖入文件夹，请使用“上传目录”按钮");
        return;
      }
      await uploadItems(plainUploadItems(droppedFiles), directory, true);
      return;
    }
    await uploadItems(await scanPromise, directory, true);
  } catch (error) {
    ElMessage.error(error.message || "拖入内容读取失败");
  } finally {
    uploadScanning.value = false;
  }
}

async function uploadItems(items, baseDirectory) {
  if (!items.files.length && !items.directories.length) {
    return;
  }
  const target = currentTarget.value;
  if (!uploadTasks.value.some((item) => ["waiting", "uploading", "paused"].includes(item.status))
    && uploadStats.value.startedAt) {
    clearUploadTasks();
  }
  const activeTasks = uploadTasks.value.filter((item) => !["canceled"].includes(item.status));
  const nextCount = activeTasks.length + items.files.length;
  const nextBytes = activeTasks.reduce((sum, item) => sum + item.total, 0)
    + items.files.reduce((sum, item) => sum + item.file.size, 0);
  if (nextCount > 1000) {
    ElMessage.error("单次最多上传 1000 个文件，请压缩文件夹后重试");
    return;
  }
  if (nextBytes > 30 * 1024 ** 3) {
    ElMessage.error("单次上传总大小不能超过 30 GB，请使用 SFTP/FTP 等工具上传");
    return;
  }
  uploadDialogVisible.value = true;
  if (!uploadTasks.value.some((item) => ["waiting", "uploading", "paused"].includes(item.status))) {
    uploadSummary.value = { directories: 0, uploaded: 0, overwritten: 0, skipped: 0, failed: 0 };
  }
  const now = Date.now();
  const tasks = items.files.map((item, index) => ({
    id: createUploadID(), uploadId: createUploadID(), target, path: joinPath(baseDirectory, item.path),
    displayPath: item.path, file: item.file, total: item.file.size, loaded: 0, speed: 0,
    status: "waiting", overwrite: false, error: null, controller: null,
    lastLoaded: 0, lastProgressAt: now, sequence: index,
  }));
  uploadTasks.value.push(...tasks);
  uploadEmptyDirectories.value.push(...(items.emptyDirectories || []).map((directory) => ({
    path: joinPath(baseDirectory, directory), target,
  })));
  uploadStats.value.totalBytes += tasks.reduce((sum, task) => sum + task.total, 0);
  uploadScanning.value = false;
  await nextTick();
}

async function renderUploadDialog() {
  await nextTick();
  await new Promise((resolve) => window.requestAnimationFrame(() => resolve()));
}

async function runUploadQueue() {
  if (uploadWorkerActive.value) return;
  if (!uploadTasks.value.some((item) => item.status === "waiting") && !uploadEmptyDirectories.value.length) return;
  uploadWorkerActive.value = true;
  if (!uploadStats.value.startedAt) uploadStats.value.startedAt = Date.now();
  try {
    await createPendingUploadDirectories();
    while (true) {
      const task = uploadTasks.value.find((item) => item.status === "waiting");
      if (!task) break;
      await runUploadTask(task);
    }
  } finally {
    uploadWorkerActive.value = false;
    if (!uploadTasks.value.some((item) => ["waiting", "uploading", "paused"].includes(item.status))) {
      showUploadSummary(uploadSummary.value);
      refreshDirectory();
    }
  }
}

async function createPendingUploadDirectories() {
  const directories = uploadEmptyDirectories.value.splice(0);
  for (const [index, directory] of directories.entries()) {
    uploadPreparing.value = `正在创建空目录 ${index + 1}/${directories.length}`;
    try {
      await fileJSON(authorization("file.create", directory.path, [], {}, directory.target), "POST", { type: "directory" });
      uploadSummary.value.directories += 1;
    } catch {
      uploadSummary.value.failed += 1;
    }
  }
  uploadPreparing.value = "";
}

async function runUploadTask(task) {
  task.status = "uploading";
  task.error = null;
  task.controller = new AbortController();
  task.lastLoaded = task.loaded;
  task.lastProgressAt = Date.now();
  try {
    await uploadFile(authorization("file.upload", task.path, [], {}, task.target), task.file, task.overwrite, {
      uploadId: task.uploadId,
      resume: task.loaded > 0 || task.hasStarted,
      signal: task.controller.signal,
      onProgress: ({ loaded }) => updateUploadProgress(task, loaded),
    });
    updateUploadProgress(task, task.total, true);
    task.hasStarted = true;
    task.speed = 0;
    task.status = "done";
    uploadStats.value.completed += 1;
    if (task.overwrite) uploadSummary.value.overwritten += 1;
    else uploadSummary.value.uploaded += 1;
  } catch (error) {
    task.hasStarted = true;
    if (error?.name === "AbortError") {
      if (task.status !== "canceled") task.status = "paused";
      return;
    }
    if (error.code === "FILE_EXISTS" && !task.overwrite) {
      const action = await conflictDialog.value.ask({
        title: "目标文件已存在",
        message: `目标位置已存在「${task.path}」，请选择处理方式。`,
        detail: "全部覆盖仅对当前上传队列后续的同名文件生效。",
        allowOverwriteAll: true,
      });
      if (action === "skip") {
        task.status = "skipped";
        uploadStats.value.completed += 1;
        uploadSummary.value.skipped += 1;
        try {
          await cancelUpload(authorization("file.upload.cancel", task.path, [], {}, task.target), task.uploadId);
        } catch { /* The upload session expires and cleans itself if the explicit cleanup is unavailable. */ }
        return;
      }
      task.overwrite = true;
      if (action === "overwrite-all") {
        for (const pending of uploadTasks.value) {
          if (pending.status === "waiting") pending.overwrite = true;
        }
      }
      task.status = "waiting";
      return;
    }
    task.status = "failed";
    uploadStats.value.completed += 1;
    task.speed = 0;
    task.error = serializeFileError(error);
    uploadSummary.value.failed += 1;
  } finally {
    task.controller = null;
  }
}

function updateUploadProgress(task, loaded, force = false) {
  const now = Date.now();
  const elapsed = Math.max(now - task.lastProgressAt, 1);
  if (!force && now - (task.lastRenderedAt || 0) < 100) return;
  const previous = task.loaded;
  task.loaded = loaded;
  task.lastRenderedAt = now;
  uploadStats.value.loadedBytes += Math.max(0, loaded - previous);
  if (elapsed >= 250 || force) {
    task.speed = Math.max(0, (loaded - task.lastLoaded) * 1000 / elapsed);
    uploadStats.value.speed = task.speed;
    task.lastLoaded = loaded;
    task.lastProgressAt = now;
  }
}

function retryUploadTask(task) {
  if (task.status !== "failed") return;
  uploadSummary.value.failed = Math.max(0, uploadSummary.value.failed - 1);
  uploadStats.value.completed = Math.max(0, uploadStats.value.completed - 1);
  task.error = null;
  task.status = "waiting";
  void runUploadQueue();
}

async function cancelUploadTask(task) {
	if (task.status === "waiting") {
    uploadTasks.value = uploadTasks.value.filter((item) => item.id !== task.id);
    uploadStats.value.totalBytes = Math.max(0, uploadStats.value.totalBytes - task.total);
    return;
	}
	const wasFailed = task.status === "failed";
	if (wasFailed) uploadSummary.value.failed = Math.max(0, uploadSummary.value.failed - 1);
  task.status = "canceled";
  if (!wasFailed) uploadStats.value.completed += 1;
  task.controller?.abort();
  try {
    await cancelUpload(authorization("file.upload.cancel", task.path, [], {}, task.target), task.uploadId);
  } catch (error) {
		if (wasFailed) uploadSummary.value.failed += 1;
    uploadStats.value.completed = Math.max(0, uploadStats.value.completed - 1);
    task.error = serializeFileError(error);
    task.status = "failed";
  }
}

function cancelAllUploads() {
  for (const task of uploadTasks.value) {
    if (task.status === "waiting") {
      task.status = "canceled";
      uploadStats.value.completed += 1;
    } else if (task.status === "uploading") {
      task.status = "canceled";
      uploadStats.value.completed += 1;
      task.controller?.abort();
    }
  }
  uploadEmptyDirectories.value = [];
}

function clearUploadTasks() {
  if (uploadWorkerActive.value) return;
  uploadTasks.value = [];
  uploadEmptyDirectories.value = [];
  uploadStats.value = { totalBytes: 0, loadedBytes: 0, completed: 0, startedAt: 0, speed: 0 };
  uploadSummary.value = { directories: 0, uploaded: 0, overwritten: 0, skipped: 0, failed: 0 };
}

function serializeFileError(error) {
  return {
    code: error?.code || "UPLOAD_FAILED", message: error?.message || "上传失败",
    stage: error?.stage || "upload", details: error?.details || [], retryable: Boolean(error?.retryable),
    requestId: error?.requestId || "",
  };
}

function showUploadSummary(state) {
  const parts = [];
  if (state.directories) parts.push("目录 " + state.directories);
  if (state.uploaded) parts.push("上传 " + state.uploaded);
  if (state.overwritten) parts.push("覆盖 " + state.overwritten);
  if (state.skipped) parts.push("跳过 " + state.skipped);
  if (state.failed) parts.push("失败 " + state.failed);
  const message = parts.length ? parts.join("，") : "未上传文件";
  if (state.failed) ElMessage.warning(message);
  else ElMessage.success(message);
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

async function showContextMenu(event, entry) {
  event.preventDefault();
  event.stopPropagation();
  if (!isSelected(entry)) selectEntry(entry, event);
  const menuWidth = 196;
  contextMenu.value = {
    visible: true,
    x: Math.max(8, Math.min(event.clientX, window.innerWidth - menuWidth - 8)),
    y: Math.max(8, Math.min(event.clientY, window.innerHeight - 48)),
    entry,
  };
  await nextTick();
  const bounds = contextMenuElement.value?.getBoundingClientRect();
  if (!bounds || contextMenu.value.entry !== entry) return;
  contextMenu.value.x = Math.max(8, Math.min(event.clientX, window.innerWidth - bounds.width - 8));
  contextMenu.value.y = Math.max(8, Math.min(event.clientY, window.innerHeight - bounds.height - 8));
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
  const isMulti = multiContextSelection.value;
  suppressEntryClick = false;
  closeContextMenu();
  if (!entry) return;
  if (action === "open") openEntry(entry);
  if (action === "online-edit") void openFile(entry);
  if (action === "open-with") void openNativeFile(entry, true);
  if (action === "download") void (isMulti ? downloadSelectedEntries() : downloadEntry(entry));
  if (action === "copy") setClipboard("copy", isMulti ? null : entry);
  if (action === "cut") setClipboard("move", isMulti ? null : entry);
  if (action === "paste") void pasteClipboard();
  if (action === "archive") void archiveEntry(entry);
  if (action === "extract") openExtractDialog(entry);
  if (action === "rename") void renameEntry(entry);
  if (action === "move") void promptMove(entry);
  if (action === "delete") void (isMulti ? removeSelectedEntries() : removeEntry(entry));
}

async function downloadEntry(entry = preview.value) {
  if (!entry.path) return;
  try {
    const name = entry.name || entry.path.split("/").pop();
    await downloadFile(
      authorization("file.download", entry.path, [], {}, currentTarget.value),
      entry.type === "directory" ? name + ".zip" : name,
    );
  } catch (error) {
    if (error?.name !== "AbortError") ElMessage.error(error.message || "文件下载失败");
  }
}

async function downloadSelectedEntries() {
  if (!selectedEntries.value.length) return;
  const entries = [...selectedEntries.value];
  const failures = [];
  for (const entry of entries) {
    try {
      const name = entry.name || entry.path.split("/").pop();
      await downloadFile(
        authorization("file.download", entry.path, [], {}, currentTarget.value),
        entry.type === "directory" ? `${name}.zip` : name,
      );
    } catch (error) {
      if (error?.name !== "AbortError") failures.push(`${entry.name}: ${error.message || "下载失败"}`);
    }
  }
  if (failures.length) ElMessage.warning(`批量下载完成，${failures.length} 项失败`);
  else ElMessage.success(`已开始下载 ${entries.length} 项`);
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
        <div class="explorer-actions">
          <button v-if="canWrite" type="button" :disabled="writeDisabled" @click="chooseFiles"><Upload :size="15" />上传文件</button>
          <button v-if="canWrite" type="button" :disabled="writeDisabled" @click="chooseDirectory"><FolderPlus :size="15" />上传目录</button>
          <button v-if="canWrite" type="button" :disabled="writeDisabled" @click="createEntry('file')"><FilePlus2 :size="15" />新建文件</button>
          <button v-if="canWrite" type="button" :disabled="writeDisabled" @click="createEntry('directory')"><FolderPlus :size="15" />新建目录</button>
          <button v-if="hasClipboard" type="button" :disabled="writeDisabled" @click="pasteClipboard"><ClipboardPaste :size="15" />粘贴</button>
          <button v-if="selectedEntries.length" type="button" :disabled="writeDisabled" @click="setClipboard('copy')"><Copy :size="15" />复制</button>
          <button v-if="selectedEntries.length" type="button" :disabled="writeDisabled" @click="setClipboard('move')"><Scissors :size="15" />剪切</button>
          <button v-if="directoryEntries.length" type="button" @click="invertSelection"><RotateCcw :size="15" />反选</button>
          <button v-if="canDelete && selectedEntries.length" class="danger" type="button" :disabled="writeDisabled" @click="removeSelectedEntries"><Trash2 :size="15" />删除</button>
          <button v-if="canDelete" type="button" :disabled="!currentTarget || recycleLoading" @click="openRecycleBin"><Trash2 :size="15" />回收站</button>
          <button v-if="canImportArchive" type="button" :disabled="writeDisabled || archiveImporting" @click="chooseArchive"><FileArchive :size="15" />导入 ZIP</button>
          <button type="button" @click="refreshDirectory"><RefreshCw :size="15" />刷新</button>
        </div>
        <button v-if="uploadTasks.length" class="upload-task-trigger" type="button" @click="uploadDialogVisible = true">
          上传任务<span v-if="activeUploadCount">{{ activeUploadCount }}</span>
        </button>
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
        <div class="file-list-head">
          <el-checkbox
            :model-value="allSelected"
            :indeterminate="selectionIndeterminate"
            aria-label="全选"
            @change="toggleAll"
          />
          <span>名称</span><span>大小</span><span>修改时间</span><span>操作</span>
        </div>
        <div v-if="!directoryLoading && directoryEntries.length === 0" class="file-list-empty">
          <FolderOpen :size="34" />
          <span>此目录为空</span>
        </div>
        <div v-else class="file-list" role="list" @click="clearSelection">
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
            <el-checkbox
              :model-value="isSelected(entry)"
              :aria-label="`选择 ${entry.name}`"
              @click.stop
              @change="toggleEntry(entry, $event)"
            />
            <span class="file-list-name">
              <Folder v-if="entry.type === 'directory'" class="folder-icon" :size="18" />
              <component :is="fileIcon(entry)" v-else :class="fileIconClass(entry)" :size="18" />
              <span>
                <button class="file-name-link" type="button" @click.stop="openEntry(entry)">{{ entry.name }}</button>
                <small>{{ entry.type === 'directory' ? '文件夹' : formatSize(entry.size) }} · {{ formatDate(entry.modified_at) }}</small>
              </span>
            </span>
            <span class="file-list-size">{{ entry.type === 'directory' ? '-' : formatSize(entry.size) }}</span>
            <time>{{ formatDate(entry.modified_at) }}</time>
            <button class="file-row-menu" type="button" aria-label="文件操作" @click.stop="showEntryMenu($event, entry)">
              <MoreHorizontal :size="17" />
            </button>
          </div>
        </div>
      </div>

      <input ref="uploadInput" type="file" multiple hidden @change="handleFileInput" />
      <input ref="uploadDirectoryInput" type="file" webkitdirectory multiple hidden @change="handleDirectoryInput" />
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
    <UploadTaskDialog
      v-model:visible="uploadDialogVisible"
      :tasks="uploadTasks"
      :stats="uploadStats"
      :scanning="uploadScanning"
      :preparing="uploadPreparing"
      :uploading="uploadWorkerActive"
      :pending-directories="uploadEmptyDirectories.length"
      :current-task-id="currentUploadTaskId"
      @choose-files="chooseFiles"
      @choose-directory="chooseDirectory"
      @start="runUploadQueue"
      @cancel="cancelUploadTask"
      @cancel-all="cancelAllUploads"
      @retry="retryUploadTask"
      @clear="clearUploadTasks"
    />
    <ExtractDialog
      v-model:visible="extractDialogVisible"
      :entry="extractEntry"
      :task="extractTask"
      @start="startExtract"
    />
    <el-dialog v-model="recycleDialogVisible" title="回收站" width="min(860px, 94vw)" @open="loadRecycleBin">
      <div v-loading="recycleLoading" class="recycle-bin-dialog">
        <el-table :data="recycleEntries" size="small" max-height="420">
          <el-table-column label="名称" min-width="150">
            <template #default="{ row }"><strong>{{ row.name }}</strong><small class="recycle-original-path">{{ row.original_path }}</small></template>
          </el-table-column>
          <el-table-column label="类型" width="90"><template #default="{ row }">{{ row.type === "directory" ? "目录" : "文件" }}</template></el-table-column>
          <el-table-column label="大小" width="100"><template #default="{ row }">{{ formatSize(row.size) }}</template></el-table-column>
          <el-table-column label="删除时间" width="170"><template #default="{ row }">{{ formatDate(row.deleted_at) }}</template></el-table-column>
          <el-table-column label="操作" width="170" align="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="restoreRecycle(row)">恢复</el-button>
              <el-button link type="danger" @click="permanentlyDeleteRecycle(row)">彻底删除</el-button>
            </template>
          </el-table-column>
          <template #empty><div class="recycle-empty"><Trash2 :size="26" /><span>回收站为空</span></div></template>
        </el-table>
      </div>
      <template #footer>
        <el-button @click="recycleDialogVisible = false">关闭</el-button>
        <el-button type="danger" plain :disabled="!recycleEntries.length || recycleLoading" @click="emptyRecycleBin">清空回收站</el-button>
      </template>
    </el-dialog>
    <Teleport to="body">
      <div
        v-if="contextMenu.visible && contextMenu.entry"
        ref="contextMenuElement"
        class="file-context-menu"
        :style="{ left: contextMenu.x + 'px', top: contextMenu.y + 'px' }"
        role="menu"
        @click.stop
        @contextmenu.prevent
      >
        <button v-if="!multiContextSelection" type="button" role="menuitem" @click="runContextAction('open')">
          <FolderOpen v-if="contextMenu.entry.type === 'directory'" :size="16" />
          <Edit3 v-else :size="16" />
          {{ contextMenu.entry.type === "directory" ? "打开文件夹" : "打开文件" }}
        </button>
        <button v-if="!multiContextSelection && isWinApp() && contextMenu.entry.type === 'file'" type="button" role="menuitem" @click="runContextAction('open-with')"><FolderOpen :size="16" />选择打开方式</button>
        <button v-if="!multiContextSelection && isWinApp() && contextMenu.entry.type === 'file'" type="button" role="menuitem" @click="runContextAction('online-edit')"><Edit3 :size="16" />在线编辑</button>
        <button type="button" role="menuitem" @click="runContextAction('download')"><Download :size="16" />{{ multiContextSelection ? `批量下载 (${selectedEntries.length})` : "下载" }}</button>
        <div class="file-context-separator" />
        <button v-if="canWrite" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('copy')"><Copy :size="16" />复制</button>
        <button v-if="canWrite" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('cut')"><Scissors :size="16" />剪切</button>
        <button v-if="hasClipboard" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('paste')"><ClipboardPaste :size="16" />粘贴</button>
        <div class="file-context-separator" />
        <template v-if="!multiContextSelection">
          <button v-if="canWrite" type="button" role="menuitem" :disabled="writeLocked || archiveCreating" @click="runContextAction('archive')"><FileArchive :size="16" />压缩为 ZIP</button>
          <button v-if="canWrite && contextMenu.entry.type === 'file' && contextMenu.entry.name.toLowerCase().endsWith('.zip')" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('extract')"><FolderOpen :size="16" />解压</button>
          <button v-if="canWrite" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('rename')"><Edit3 :size="16" />重命名</button>
          <button v-if="canWrite" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('move')"><MoveRight :size="16" />移动到</button>
        </template>
        <template v-if="canDelete">
          <div class="file-context-separator" />
          <button class="danger" type="button" role="menuitem" :disabled="writeLocked" @click="runContextAction('delete')"><Trash2 :size="16" />{{ multiContextSelection ? `批量删除 (${selectedEntries.length})` : "删除" }}</button>
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
.recycle-bin-dialog { min-height: 120px; }
.recycle-original-path { display: block; margin-top: 3px; overflow: hidden; color: var(--app-text-muted); font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.recycle-empty { display: grid; justify-items: center; gap: 8px; padding: 32px 0; color: var(--app-text-muted); }
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
.explorer-heading { display: flex; min-height: 46px; align-items: center; justify-content: space-between; gap: 10px; padding: 6px 9px; border-bottom: 1px solid var(--app-border-soft); }
.explorer-actions, .editor-commands { display: flex; align-items: center; gap: 4px; }
.explorer-actions { min-width: 0; flex-wrap: wrap; }
.explorer-actions button { display: flex; height: 30px; align-items: center; gap: 5px; border: 1px solid var(--app-border); border-radius: 4px; padding: 0 9px; background: var(--app-surface); color: var(--app-text-secondary); cursor: pointer; font-size: 11px; white-space: nowrap; }
.editor-commands button, .editor-tab button {
  display: grid; width: 30px; height: 30px; place-items: center; border: 1px solid transparent; border-radius: 4px; padding: 0; background: transparent; color: var(--app-text-secondary); cursor: pointer;
}
.explorer-actions button:hover, .editor-commands button:hover, .editor-tab button:hover { border-color: var(--app-border); background: var(--app-surface-hover); color: var(--app-text); }
.explorer-actions button:disabled, .editor-commands button:disabled { cursor: not-allowed; opacity: 0.38; }
.upload-task-trigger { display: flex; height: 30px; flex: 0 0 auto; align-items: center; gap: 6px; border: 1px solid var(--file-accent); border-radius: 4px; padding: 0 9px; color: var(--file-accent-text); background: transparent; font-size: 11px; }
.upload-task-trigger span { display: grid; min-width: 18px; height: 18px; place-items: center; border-radius: 9px; color: #fff; background: var(--file-accent); font-size: 9px; }
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
.file-list-head, .file-list-row { display: grid; grid-template-columns: 28px minmax(240px, 1fr) 110px 170px 38px; align-items: center; gap: 8px; }
.file-list-head { min-height: 34px; padding: 0 9px 0 14px; color: var(--app-text-muted); background: var(--app-surface-muted); border-bottom: 1px solid var(--app-border-soft); font-size: 10px; font-weight: 700; }
.file-list { height: calc(100% - 34px); overflow: auto; overscroll-behavior: contain; }
.file-list-row { height: 36px; min-height: 36px; padding: 0 9px 0 14px; border-bottom: 1px solid var(--app-border-soft); color: var(--app-text-secondary); cursor: default; user-select: none; }
.file-list-row:hover, .file-list-row:focus-visible { background: var(--app-surface-hover); outline: 0; }
.file-list-row.selected { background: var(--file-selected-bg); box-shadow: inset 3px 0 var(--file-accent); }
.file-list-row.drop-target { background: var(--file-drop-bg); box-shadow: inset 0 0 0 1px var(--file-accent); }
.file-list-row.selected:hover, .file-list-row.selected:focus-visible { background: var(--file-selected-bg); }
.file-list-row.drop-target:hover, .file-list-row.drop-target:focus-visible { background: var(--file-drop-bg); }
.file-list-name { display: flex; min-width: 0; align-items: center; gap: 9px; }
.file-list-name > svg { flex: 0 0 auto; }
.file-list-name > span { min-width: 0; }
.file-list-name .file-name-link, .file-list-name small { display: block; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-name-link { border: 0; padding: 0; color: #3570a3; background: transparent; cursor: pointer; font-size: 12px; font-weight: 600; text-align: left; }
.file-name-link:hover { color: #225884; text-decoration: underline; }
:global(html.dark) .file-name-link { color: #85b8e0; }
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
.file-context-menu { --file-menu-danger: #a23f36; position: fixed; z-index: 5000; display: grid; width: 196px; max-height: calc(100vh - 16px); overflow-x: hidden; overflow-y: auto; overscroll-behavior: contain; padding: 5px; color: var(--app-text); background: var(--app-surface); border: 1px solid var(--app-border); border-radius: 6px; box-shadow: var(--app-shadow); }
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
  .file-list-row { grid-template-columns: 26px minmax(0, 1fr) 36px; min-height: 58px; gap: 6px; padding: 6px 8px; touch-action: pan-y; }
  .file-list-name small { display: block; }
  .file-list-size, .file-list-row > time { display: none; }
  .file-row-menu { width: 36px; height: 36px; }
  .editor-pane { min-height: 0; }
  .editor-tab { min-width: 0; max-width: calc(100vw - 110px); }
  .editor-statusbar { gap: 9px; }
}
</style>
