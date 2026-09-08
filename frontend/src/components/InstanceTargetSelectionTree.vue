<script setup>
import { computed } from "vue";
import { Boxes, Server } from "lucide-vue-next";

const props = defineProps({
  modelValue: { type: Array, default: () => [] },
  nodes: { type: Array, default: () => [] },
  source: { type: Object, default: () => ({}) },
  disabled: { type: Boolean, default: false },
});
const emit = defineEmits(["update:modelValue"]);

const groups = computed(() => props.nodes.map((content) => ({
  node: content.node,
  servers: (content.servers || []).map((server) => ({
    ...server,
    instances: (content.instances || []).filter((instance) => instance.server_id === server.server_id),
  })).filter((server) => server.instances.length),
})).filter((content) => content.servers.length));

function key(nodeId, instanceId) { return `${nodeId}\x00${instanceId}`; }
function selected(nodeId, instanceId) {
  return props.modelValue.some((item) => item.node_id === nodeId && item.instance_id === instanceId);
}
function sourceSelected(nodeId, instanceId) {
  return props.source.resource_type === "instance"
    && props.source.node_id === nodeId && props.source.resource_id === instanceId;
}
function instances(content) { return content.servers.flatMap((server) => server.instances); }
function nodeChecked(content) {
  const values = instances(content).filter((item) => !sourceSelected(content.node.id, item.instance_id));
  return values.length > 0 && values.every((item) => selected(content.node.id, item.instance_id));
}
function nodeIndeterminate(content) {
  const values = instances(content).filter((item) => !sourceSelected(content.node.id, item.instance_id));
  const count = values.filter((item) => selected(content.node.id, item.instance_id)).length;
  return count > 0 && count < values.length;
}
function update(next) { emit("update:modelValue", next); }
function setInstance(nodeId, instance, enabled) {
  if (sourceSelected(nodeId, instance.instance_id)) return;
  const next = props.modelValue.filter((item) => key(item.node_id, item.instance_id) !== key(nodeId, instance.instance_id));
  if (enabled) next.push({ node_id: nodeId, instance_id: instance.instance_id });
  update(next);
}
function setNode(content, enabled) {
  const ids = new Set(instances(content).map((item) => key(content.node.id, item.instance_id)));
  let next = props.modelValue.filter((item) => !ids.has(key(item.node_id, item.instance_id)));
  if (enabled) {
    next = next.concat(instances(content)
      .filter((item) => !sourceSelected(content.node.id, item.instance_id))
      .map((item) => ({ node_id: content.node.id, instance_id: item.instance_id })));
  }
  update(next);
}
</script>

<template>
  <div class="instance-target-tree">
    <div v-for="content in groups" :key="content.node.id" class="target-node">
      <div class="target-node-head">
        <el-checkbox
          :model-value="nodeChecked(content)"
          :indeterminate="nodeIndeterminate(content)"
          :disabled="disabled"
          @change="setNode(content, $event)"
        >
          <span class="target-node-name"><Boxes :size="15" />{{ content.node.name }}</span>
        </el-checkbox>
        <small>{{ instances(content).length }} 个子服</small>
      </div>
      <div v-for="server in content.servers" :key="server.server_id" class="target-server-group">
        <div class="target-server-title"><Server :size="14" />{{ server.name || server.server_id }}</div>
        <el-checkbox
          v-for="instance in server.instances"
          :key="instance.instance_id"
          :model-value="selected(content.node.id, instance.instance_id)"
          :disabled="disabled || sourceSelected(content.node.id, instance.instance_id)"
          @change="setInstance(content.node.id, instance, $event)"
        >
          <span class="target-instance-name">{{ instance.name || instance.instance_id }}</span>
          <small>{{ instance.instance_id }} · {{ instance.state || "未知" }}</small>
        </el-checkbox>
      </div>
    </div>
    <div v-if="!groups.length" class="target-tree-empty">没有可选择的子服</div>
  </div>
</template>

<style scoped>
.instance-target-tree { border: 1px solid var(--el-border-color); max-height: 390px; overflow: auto; }
.target-node + .target-node { border-top: 1px solid var(--el-border-color-lighter); }
.target-node-head { min-height: 44px; padding: 0 12px; display: flex; align-items: center; justify-content: space-between; background: var(--el-fill-color-lighter); }
.target-node-head small, .target-instance-name + small { color: var(--el-text-color-secondary); }
.target-node-name, .target-server-title { display: inline-flex; align-items: center; gap: 7px; }
.target-server-group { display: grid; }
.target-server-title { padding: 8px 14px; color: var(--el-text-color-secondary); background: var(--el-fill-color-extra-light); font-size: 12px; }
.target-server-group :deep(.el-checkbox) { width: 100%; min-height: 42px; margin: 0; padding: 0 14px 0 38px; border-top: 1px solid var(--el-border-color-extra-light); }
.target-server-group :deep(.el-checkbox__label) { width: 100%; display: flex; align-items: center; justify-content: space-between; gap: 16px; overflow: hidden; }
.target-server-group small { white-space: nowrap; }
.target-tree-empty { padding: 28px; text-align: center; color: var(--el-text-color-secondary); }
</style>
