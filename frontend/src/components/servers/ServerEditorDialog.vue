<script setup>
import { computed, reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  server: { type: Object, default: null },
  nodeId: { type: String, default: "" },
  nodes: { type: Array, default: () => [] },
  submitting: { type: Boolean, default: false },
});
const emit = defineEmits(["update:modelValue", "submit"]);

const formRef = ref();
const form = reactive({
  nodeId: "",
  type: "standalone",
  serverId: "",
  name: "",
  workspace: "",
  port: 25565,
  rootPath: "",
  imageDirectory: "image",
  instanceCount: 1,
  portsText: "25565",
  startCommand: "java -jar server.jar nogui",
  stopCommand: "stop",
  stopTimeoutSeconds: 60,
  autoStart: false,
  autoRestart: false,
  encoding: "utf-8",
});

const open = computed({
  get: () => props.modelValue,
  set: (value) => emit("update:modelValue", value),
});
const editing = computed(() => Boolean(props.server));
const availableNodes = computed(() => props.nodes.map((item) => item.node || item));
const typeOptions = [
  { label: "固定实例", value: "standalone" },
  { label: "镜像组", value: "mirror" },
];
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
  Object.assign(form, {
    nodeId: props.nodeId || firstOnline?.id || availableNodes.value[0]?.id || "",
    type: source?.type || "standalone",
    serverId: source?.server_id || "",
    name: source?.name || "",
    workspace: source?.workspace || "",
    port: source?.port || 25565,
    rootPath: source?.root_path || "",
    imageDirectory: source?.image_directory || "image",
    instanceCount: source?.instance_count || 1,
    portsText: source?.ports?.join(", ") || "25565",
    startCommand: source?.process?.start_command || "java -jar server.jar nogui",
    stopCommand: source?.process?.stop_command || "stop",
    stopTimeoutSeconds: source?.process?.stop_timeout_seconds || 60,
    autoStart: source?.process?.auto_start || false,
    autoRestart: source?.process?.auto_restart || false,
    encoding: source?.console?.encoding || "utf-8",
  });
  formRef.value?.clearValidate();
}

watch(() => props.modelValue, (value) => {
  if (value) resetForm();
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

async function submit() {
  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) return;
  if (form.type === "standalone") {
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
  if (form.type === "mirror") {
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
  }
  const config = {
    schema_version: 1,
    type: form.type,
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
  if (form.type === "standalone") {
    Object.assign(config, { workspace: form.workspace.trim(), port: form.port });
  } else {
    Object.assign(config, {
      root_path: form.rootPath.trim(),
      image_directory: form.imageDirectory.trim(),
      instance_count: form.instanceCount,
      ports: ports.slice(0, form.instanceCount),
      exclude: props.server?.exclude || [],
    });
  }
  emit("submit", { nodeId: form.nodeId, config });
}
</script>

<template>
  <el-dialog v-model="open" :title="editing ? '编辑服务器' : '新增服务器'" width="720px">
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
      <el-form-item label="服务器类型">
        <el-segmented v-model="form.type" :options="typeOptions" :disabled="editing" />
      </el-form-item>
      <div class="dialog-form-grid">
        <el-form-item label="服务器 ID" prop="serverId">
          <el-input v-model="form.serverId" :disabled="editing" placeholder="survival" />
        </el-form-item>
        <el-form-item label="显示名称" prop="name">
          <el-input v-model="form.name" maxlength="100" placeholder="生存服" />
        </el-form-item>
      </div>

      <template v-if="form.type === 'standalone'">
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
      </template>

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
      <el-button @click="open = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">保存</el-button>
    </template>
  </el-dialog>
</template>
