<script setup>
import { computed } from "vue";
import { Boxes, Server } from "lucide-vue-next";

const props = defineProps({
  modelValue: { type: Array, default: () => [] },
  nodes: { type: Array, default: () => [] },
  pluginType: { type: String, default: "" },
  excludeProxy: { type: Boolean, default: false },
  proxyOnly: { type: Boolean, default: false },
  disabled: { type: Boolean, default: false },
});
const emit = defineEmits(["update:modelValue"]);

function platformPluginType(platform) {
  if (platform === "velocity" || platform === "bungee") return platform;
  if (platform === "fabric" || platform === "forge") return platform;
  return "spigot";
}

const treeNodes = computed(() => props.nodes.map((content) => ({
  node: content.node,
  servers: (content.servers || []).filter((server) => {
    const proxy = ["velocity", "bungee"].includes(server.platform);
    if (props.excludeProxy && proxy) return false;
    if (props.proxyOnly && !proxy) return false;
    return !props.pluginType || platformPluginType(server.platform) === props.pluginType;
  }),
})).filter((content) => content.servers.length));

function nodeRule(nodeId) {
  return props.modelValue.find((rule) => rule.node_id === nodeId && !rule.server_id);
}

function serverRule(nodeId, serverId) {
  return props.modelValue.find((rule) => rule.node_id === nodeId && rule.server_id === serverId);
}

function serverSelected(nodeId, serverId) {
  return serverRule(nodeId, serverId)?.enabled ?? nodeRule(nodeId)?.enabled ?? false;
}

function nodeState(content) {
  const values = content.servers.map((server) => serverSelected(content.node.id, server.server_id));
  return {
    checked: values.length > 0 && values.every(Boolean),
    indeterminate: values.some(Boolean) && !values.every(Boolean),
  };
}

function updateRules(next) {
  emit("update:modelValue", next);
}

function setNode(content, enabled) {
  const next = props.modelValue.filter((rule) => rule.node_id !== content.node.id);
  next.push({ node_id: content.node.id, server_id: "", enabled });
  updateRules(next);
}

function setServer(nodeId, serverId, enabled) {
  const next = props.modelValue.filter((rule) => !(
    rule.node_id === nodeId && rule.server_id === serverId
  ));
  next.push({ node_id: nodeId, server_id: serverId, enabled });
  updateRules(next);
}
</script>

<template>
  <div class="target-tree">
    <div v-for="content in treeNodes" :key="content.node.id" class="target-node">
      <div class="target-node-head">
        <el-checkbox
          :model-value="nodeState(content).checked"
          :indeterminate="nodeState(content).indeterminate"
          :disabled="disabled"
          @change="setNode(content, $event)"
        >
          <span class="target-node-name"><Boxes :size="15" />{{ content.node.name }}</span>
        </el-checkbox>
        <small>{{ content.servers.length }} 个服务器</small>
      </div>
      <div class="target-server-list">
        <el-checkbox
          v-for="server in content.servers"
          :key="server.server_id"
          :model-value="serverSelected(content.node.id, server.server_id)"
          :disabled="disabled"
          @change="setServer(content.node.id, server.server_id, $event)"
        >
          <span class="target-server-name"><Server :size="14" />{{ server.name }}</span>
          <small>{{ server.server_id }} · {{ server.platform || "paper" }}</small>
        </el-checkbox>
      </div>
    </div>
    <div v-if="!treeNodes.length" class="target-tree-empty">没有兼容的服务器</div>
  </div>
</template>

<style scoped>
.target-tree { border: 1px solid var(--el-border-color); max-height: 360px; overflow: auto; }
.target-node + .target-node { border-top: 1px solid var(--el-border-color-lighter); }
.target-node-head { min-height: 44px; padding: 0 12px; display: flex; align-items: center; justify-content: space-between; background: var(--el-fill-color-lighter); }
.target-node-head small, .target-server-name + small { color: var(--el-text-color-secondary); }
.target-node-name, .target-server-name { display: inline-flex; align-items: center; gap: 7px; }
.target-server-list { display: grid; }
.target-server-list :deep(.el-checkbox) { width: 100%; min-height: 42px; margin: 0; padding: 0 14px 0 38px; border-top: 1px solid var(--el-border-color-extra-light); }
.target-server-list :deep(.el-checkbox__label) { width: 100%; display: flex; align-items: center; justify-content: space-between; gap: 16px; overflow: hidden; }
.target-server-list small { white-space: nowrap; }
.target-tree-empty { padding: 28px; text-align: center; color: var(--el-text-color-secondary); }
</style>
