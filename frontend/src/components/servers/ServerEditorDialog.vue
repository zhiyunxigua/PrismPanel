<script setup>
import { computed, nextTick, reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { FileArchive, Plus, Trash2 } from "lucide-vue-next";
import TargetSelectionTree from "../TargetSelectionTree.vue";
import { request } from "../../api";
import { hasPermission } from "../../session";

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  server: { type: Object, default: null },
  nodeId: { type: String, default: "" },
  nodes: { type: Array, default: () => [] },
  nodeContents: { type: Array, default: () => [] },
  submitting: { type: Boolean, default: false },
});
const emit = defineEmits(["update:modelValue", "submit"]);

const defaultPluginConfigSyncExtensions = [
  ".yml", ".yaml", ".json", ".toml", ".ini", ".conf", ".properties", ".xml",
];

const formRef = ref();
const archiveUploadRef = ref();
const archiveFile = ref(null);
const proxyTargetsLoading = ref(false);
const proxyTargetsReady = ref(true);
const proxyTargetDefaults = ref(new Map());
let proxyTargetRequest = 0;
let resettingForm = false;
const form = reactive({
  nodeId: "",
  kind: "ordinary",
  platform: "paper",
  serverId: "",
  name: "",
  workspace: "",
  port: 25565,
  rootPath: "",
  imageDirectory: "image",
  instanceCount: 1,
  portsText: "25565",
  exclude: [],
  pluginConfigSyncExtensions: [...defaultPluginConfigSyncExtensions],
  startCommand: "java -jar server.jar nogui",
  stopCommand: "stop",
  stopTimeoutSeconds: 60,
  autoStart: false,
  autoRestart: false,
  encoding: "utf-8",
  proxyRules: [],
  proxyTargets: [],
});

const open = computed({
  get: () => props.modelValue,
  set: (value) => {
    if (!value && props.submitting) return;
    emit("update:modelValue", value);
  },
});
const editing = computed(() => Boolean(props.server));
const canConfigureProxy = computed(() => hasPermission("server.configure"));
const availableNodes = computed(() => props.nodes.map((item) => item.node || item));
const typeOptions = [
  { label: "普通服务器", value: "ordinary" },
  { label: "镜像服", value: "mirror" },
  { label: "代理服", value: "proxy" },
];
const platformOptions = computed(() => (
  form.kind === "proxy"
    ? [{ label: "Velocity", value: "velocity" }, { label: "BungeeCord", value: "bungee" }]
    : [{ label: "Paper", value: "paper" }, { label: "Spigot", value: "spigot" }]
));
const proxyOptions = computed(() => props.nodeContents.flatMap((content) => (
  (content.servers || [])
    .filter((server) => ["velocity", "bungee"].includes(server.platform))
    .map((server) => ({
      node_id: content.node.id,
      server_id: server.server_id,
    }))
)));

function proxyTargetKey(nodeId, serverId) {
  return nodeId + "\x00" + serverId;
}

function selectedByTargetRules(rules, nodeId, serverId) {
  const serverRule = rules.find((rule) => rule.node_id === nodeId && rule.server_id === serverId);
  if (serverRule) return Boolean(serverRule.enabled);
  const nodeRule = rules.find((rule) => rule.node_id === nodeId && !rule.server_id);
  return Boolean(nodeRule?.enabled);
}

function normalizeExcludeEntries(entries) {
  if (!Array.isArray(entries)) return [];
  return entries.map((entry) => ({
    type: entry?.type === "file" ? "file" : "directory",
    path: String(entry?.path || ""),
  }));
}

function addExcludeEntry() {
  form.exclude.push({ type: "directory", path: "" });
}

function removeExcludeEntry(index) {
  form.exclude.splice(index, 1);
}

async function loadProxyTargetDefaults() {
  const requestID = ++proxyTargetRequest;
  form.proxyTargets = [];
  proxyTargetDefaults.value = new Map();
  if (editing.value || form.kind === "proxy" || !form.nodeId || !canConfigureProxy.value || !proxyOptions.value.length) {
    proxyTargetsLoading.value = false;
    proxyTargetsReady.value = true;
    return;
  }
  proxyTargetsLoading.value = true;
  proxyTargetsReady.value = false;
  try {
    const query = "?target_node_id=" + encodeURIComponent(form.nodeId);
    const data = await request("/api/v1/proxy-sync-rules" + query);
    if (requestID !== proxyTargetRequest) return;
    const selected = new Map((data.proxies || []).map((item) => (
      [proxyTargetKey(item.node_id, item.server_id), Boolean(item.selected)]
    )));
    const defaults = new Map();
    form.proxyTargets = proxyOptions.value.map((item) => {
      const enabled = selected.get(proxyTargetKey(item.node_id, item.server_id)) ?? false;
      defaults.set(proxyTargetKey(item.node_id, item.server_id), enabled);
      return { node_id: item.node_id, server_id: item.server_id, enabled };
    });
    proxyTargetDefaults.value = defaults;
    proxyTargetsReady.value = true;
  } catch (error) {
    if (requestID === proxyTargetRequest) {
      ElMessage.error("读取代理服默认同步规则失败：" + error.message);
    }
  } finally {
    if (requestID === proxyTargetRequest) proxyTargetsLoading.value = false;
  }
}

const rules = {
  nodeId: [{ required: true, message: "请选择目标节点", trigger: "change" }],
  serverId: [
    { required: true, message: "请输入服务器 ID", trigger: "blur" },
    { pattern: /^[a-z0-9_-]+$/, message: "仅允许小写字母、数字、- 和 _", trigger: "blur" },
  ],
  name: [{ required: true, message: "请输入显示名称", trigger: "blur" }],
  startCommand: [{ required: true, message: "请输入启动命令", trigger: "blur" }],
  stopCommand: [{ required: true, message: "请输入停止命令", trigger: "blur" }],
};

function resetForm() {
  const source = props.server;
  const firstOnline = availableNodes.value.find((node) => node.status === "ONLINE");
  resettingForm = true;
  Object.assign(form, {
    nodeId: props.nodeId || firstOnline?.id || availableNodes.value[0]?.id || "",
    kind: source?.type === "mirror"
      ? "mirror"
      : (["velocity", "bungee"].includes(source?.platform) ? "proxy" : "ordinary"),
    platform: source?.platform || "paper",
    serverId: source?.server_id || "",
    name: source?.name || "",
    workspace: source?.workspace || "",
    port: source?.port || 25565,
    rootPath: source?.root_path || "",
    imageDirectory: source?.image_directory || "image",
    instanceCount: source?.instance_count || 1,
    portsText: source?.ports?.join(", ") || "25565",
    exclude: normalizeExcludeEntries(source?.exclude),
    pluginConfigSyncExtensions: source?.plugin_config_sync_extensions?.length
      ? [...source.plugin_config_sync_extensions]
      : [...defaultPluginConfigSyncExtensions],
    startCommand: source?.process?.start_command || "java -jar server.jar nogui",
    stopCommand: source?.process?.stop_command || "stop",
    stopTimeoutSeconds: source?.process?.stop_timeout_seconds || 60,
    autoStart: source?.process?.auto_start || false,
    autoRestart: source?.process?.auto_restart || false,
    encoding: source?.console?.encoding || "utf-8",
    proxyRules: [],
    proxyTargets: [],
  });
  resettingForm = false;
  archiveFile.value = null;
  nextTick(() => archiveUploadRef.value?.clearFiles());
  formRef.value?.clearValidate();
}

watch(() => props.modelValue, (value) => {
  if (value) {
    resetForm();
    void loadProxyTargetDefaults();
  }
});
watch(() => form.kind, (value) => {
  if (value === "proxy" && !["velocity", "bungee"].includes(form.platform)) form.platform = "velocity";
  if (value !== "proxy" && !["paper", "spigot"].includes(form.platform)) form.platform = "paper";
  if (open.value && !editing.value && !resettingForm) void loadProxyTargetDefaults();
});
watch(() => form.nodeId, () => {
  if (open.value && !editing.value && form.kind !== "proxy" && !resettingForm) {
    void loadProxyTargetDefaults();
  }
});
watch(() => proxyOptions.value.map((item) => proxyTargetKey(item.node_id, item.server_id)).join("|"), () => {
  if (open.value && !editing.value && form.kind !== "proxy") void loadProxyTargetDefaults();
});

function parsePorts() {
  return form.portsText
    .split(/[\s,，]+/)
    .filter(Boolean)
    .map((value) => Number(value));
}

function validPort(value) {
  return Number.isInteger(value) && value >= 1 && value <= 65535;
}

function handleArchiveChange(uploadFile) {
  const file = uploadFile.raw;
  if (!file?.name.toLowerCase().endsWith(".zip")) {
    archiveFile.value = null;
    archiveUploadRef.value?.clearFiles();
    ElMessage.warning("初始化文件仅支持 ZIP 压缩包");
    return;
  }
  archiveFile.value = file;
}

function handleArchiveRemove() {
  archiveFile.value = null;
}

function handleArchiveExceed(files) {
  archiveUploadRef.value?.clearFiles();
  if (files[0]) archiveUploadRef.value?.handleStart(files[0]);
}

function beforeClose(done) {
  if (!props.submitting) done();
}

async function submit() {
  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) return;
  if (form.kind !== "mirror") {
    if (!form.workspace.trim()) {
      ElMessage.warning("请输入服务器工作目录");
      return;
    }
    if (!validPort(form.port)) {
      ElMessage.warning("端口必须在 1 到 65535 之间");
      return;
    }
  }
  const ports = parsePorts();
  if (form.kind === "mirror") {
    if (!form.rootPath.trim() || !form.imageDirectory.trim()) {
      ElMessage.warning("请输入镜像组根目录和镜像目录");
      return;
    }
    if (!Number.isInteger(form.instanceCount) || form.instanceCount < 1) {
      ElMessage.warning("实例数量必须大于 0");
      return;
    }
    if (ports.length < form.instanceCount || ports.some((port) => !validPort(port))) {
      ElMessage.warning("请提供足够数量的有效实例端口");
      return;
    }
    if (form.exclude.some((entry) => !entry.path.trim())) {
      ElMessage.warning("请填写排除项路径，或删除空白排除项");
      return;
    }
    if (!form.pluginConfigSyncExtensions.length) {
      ElMessage.warning("请至少保留一个插件配置文件后缀");
      return;
    }
  }
  let proxyTargets = null;
  if (!editing.value && form.kind !== "proxy" && canConfigureProxy.value) {
    if (proxyTargetsLoading.value || !proxyTargetsReady.value) {
      ElMessage.warning("代理服同步规则尚未读取完成");
      return;
    }
    proxyTargets = proxyOptions.value.flatMap((item) => {
      const key = proxyTargetKey(item.node_id, item.server_id);
      const selected = selectedByTargetRules(form.proxyTargets, item.node_id, item.server_id);
      const inherited = proxyTargetDefaults.value.get(key) ?? false;
      return selected === inherited
        ? []
        : [{ node_id: item.node_id, server_id: item.server_id, enabled: selected }];
    });
  }
  const config = {
    schema_version: 2,
    type: form.kind === "mirror" ? "mirror" : "standalone",
    platform: form.platform,
    server_id: form.serverId,
    name: form.name.trim(),
    process: {
      start_command: form.startCommand.trim(),
      stop_command: form.stopCommand.trim(),
      stop_timeout_seconds: form.stopTimeoutSeconds,
      auto_start: form.autoStart,
      auto_restart: form.autoRestart,
    },
    console: { encoding: form.encoding },
  };
  if (form.kind !== "mirror") {
    Object.assign(config, { workspace: form.workspace.trim(), port: form.port });
  } else {
    Object.assign(config, {
      root_path: form.rootPath.trim(),
      image_directory: form.imageDirectory.trim(),
      instance_count: form.instanceCount,
      ports: ports.slice(0, form.instanceCount),
      exclude: form.exclude.map((entry) => ({
        type: entry.type,
        path: entry.path.trim(),
      })),
      plugin_config_sync_extensions: form.pluginConfigSyncExtensions,
    });
  }
  emit("submit", {
    nodeId: form.nodeId,
    config,
    archive: archiveFile.value,
    proxyRules: canConfigureProxy.value ? form.proxyRules : null,
    proxyTargets,
  });
}
</script>

<template>
  <el-dialog
    v-model="open"
    :title="editing ? '编辑服务器' : '新增服务器'"
    width="720px"
    :before-close="beforeClose"
    :close-on-click-modal="!submitting"
    :close-on-press-escape="!submitting"
    :show-close="!submitting"
  >
    <el-form ref="formRef" :model="form" :rules="rules" label-position="top">
      <el-form-item v-if="!editing" label="目标节点" prop="nodeId">
        <el-select v-model="form.nodeId" class="full-control" placeholder="选择节点">
          <el-option
            v-for="node in availableNodes"
            :key="node.id"
            :label="node.name + (node.status === 'ONLINE' ? '' : ' · ' + node.status)"
            :value="node.id"
          />
        </el-select>
      </el-form-item>
      <div class="dialog-form-grid">
        <el-form-item label="服务器类型">
          <el-segmented v-model="form.kind" :options="typeOptions" :disabled="editing" />
        </el-form-item>
        <el-form-item label="服务端平台">
          <el-select v-model="form.platform" class="full-control" :disabled="editing">
            <el-option v-for="item in platformOptions" :key="item.value" :label="item.label" :value="item.value" />
          </el-select>
        </el-form-item>
      </div>
      <div class="dialog-form-grid">
        <el-form-item label="服务器 ID" prop="serverId">
          <el-input v-model="form.serverId" :disabled="editing" placeholder="survival" />
        </el-form-item>
        <el-form-item label="显示名称" prop="name">
          <el-input v-model="form.name" maxlength="100" placeholder="生存服" />
        </el-form-item>
      </div>

      <template v-if="form.kind !== 'mirror'">
        <el-form-item label="工作目录">
          <el-input v-model="form.workspace" placeholder="/srv/minecraft/survival" />
        </el-form-item>
        <el-form-item label="服务端口">
          <el-input-number v-model="form.port" :min="1" :max="65535" controls-position="right" />
        </el-form-item>
      </template>
      <template v-else>
        <el-form-item label="服务器组根目录">
          <el-input v-model="form.rootPath" placeholder="/srv/minecraft/bedwars" />
        </el-form-item>
        <div class="dialog-form-grid">
          <el-form-item label="镜像目录">
            <el-input v-model="form.imageDirectory" placeholder="image" />
          </el-form-item>
          <el-form-item label="实例数量">
            <el-input-number v-model="form.instanceCount" :min="1" :max="100" controls-position="right" />
          </el-form-item>
        </div>
        <el-form-item label="实例端口">
          <el-input v-model="form.portsText" placeholder="25565, 25566, 25567" />
        </el-form-item>
        <el-form-item label="部署排除项">
          <div class="exclude-editor">
            <div v-for="(entry, index) in form.exclude" :key="index" class="exclude-row">
              <el-select v-model="entry.type" class="exclude-type" :disabled="submitting">
                <el-option label="目录（保留整个目录）" value="directory" />
                <el-option label="文件（保留单个文件）" value="file" />
              </el-select>
              <el-input
                v-model="entry.path"
                :disabled="submitting"
                placeholder="例如 world 或 server.properties"
              />
              <el-tooltip content="删除排除项">
                <el-button
                  class="square-button"
                  type="danger"
                  plain
                  :disabled="submitting"
                  aria-label="删除排除项"
                  @click="removeExcludeEntry(index)"
                >
                  <Trash2 :size="15" />
                </el-button>
              </el-tooltip>
            </div>
            <el-button plain :disabled="submitting" @click="addExcludeEntry">
              <Plus :size="15" />添加排除项
            </el-button>
            <small class="form-help">路径相对于每个实例目录；排除目录会保留其全部子目录和文件。</small>
          </div>
        </el-form-item>
        <el-form-item label="插件配置同步后缀白名单">
          <el-select
            v-model="form.pluginConfigSyncExtensions"
            class="full-control"
            multiple
            filterable
            allow-create
            default-first-option
            collapse-tags
            collapse-tags-tooltip
            placeholder="输入后缀并回车，例如 .yml"
          >
            <el-option v-for="extension in defaultPluginConfigSyncExtensions" :key="extension" :label="extension" :value="extension" />
          </el-select>
          <small class="form-help">仅同步这些后缀的插件配置文件，不会删除目标中的额外文件；数据库后缀请勿加入白名单。</small>
        </el-form-item>
      </template>

      <el-form-item v-if="!editing && form.kind === 'proxy' && canConfigureProxy" label="初始下游服务器">
        <TargetSelectionTree v-model="form.proxyRules" :nodes="nodeContents" exclude-proxy :disabled="submitting" />
      </el-form-item>
      <el-form-item
        v-if="!editing && form.kind !== 'proxy' && canConfigureProxy && proxyOptions.length"
        label="同步到代理服"
      >
        <div v-loading="proxyTargetsLoading" class="full-control">
          <TargetSelectionTree
            v-model="form.proxyTargets"
            :nodes="nodeContents"
            proxy-only
            :disabled="submitting || proxyTargetsLoading"
          />
        </div>
      </el-form-item>

      <el-form-item v-if="!editing" label="初始化文件">
        <el-upload
          ref="archiveUploadRef"
          class="archive-upload"
          accept=".zip,application/zip"
          :auto-upload="false"
          :limit="1"
          :disabled="submitting"
          :on-change="handleArchiveChange"
          :on-remove="handleArchiveRemove"
          :on-exceed="handleArchiveExceed"
        >
          <el-button :disabled="submitting">
            <FileArchive :size="16" />
            选择 ZIP
          </el-button>
        </el-upload>
      </el-form-item>

      <el-divider content-position="left">进程与控制台</el-divider>
      <el-form-item label="启动命令" prop="startCommand">
        <el-input v-model="form.startCommand" type="textarea" :rows="2" />
      </el-form-item>
      <div class="dialog-form-grid">
        <el-form-item label="停止命令" prop="stopCommand">
          <el-input v-model="form.stopCommand" />
        </el-form-item>
        <el-form-item label="停止超时（秒）">
          <el-input-number v-model="form.stopTimeoutSeconds" :min="1" :max="3600" controls-position="right" />
        </el-form-item>
      </div>
      <div class="dialog-form-grid">
        <el-form-item label="控制台编码">
          <el-select v-model="form.encoding" class="full-control">
            <el-option label="UTF-8" value="utf-8" />
            <el-option label="GBK" value="gbk" />
          </el-select>
        </el-form-item>
        <el-form-item label="自动策略">
          <div class="switch-row">
            <el-checkbox v-model="form.autoStart">daemon 启动后运行</el-checkbox>
            <el-checkbox v-model="form.autoRestart">异常退出后重启</el-checkbox>
          </div>
        </el-form-item>
      </div>
    </el-form>
    <template #footer>
      <el-button :disabled="submitting" @click="open = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">保存</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.form-help { display: block; margin-top: 6px; color: var(--app-text-muted); font-size: 12px; line-height: 1.5; }
.exclude-editor { display: grid; gap: 8px; width: 100%; }
.exclude-row { display: grid; grid-template-columns: 190px minmax(0, 1fr) 32px; gap: 8px; align-items: start; }
.exclude-type { min-width: 0; }

@media (max-width: 560px) {
  .exclude-row { grid-template-columns: minmax(0, 1fr) 32px; }
  .exclude-type { grid-column: 1 / -1; }
}
</style>
