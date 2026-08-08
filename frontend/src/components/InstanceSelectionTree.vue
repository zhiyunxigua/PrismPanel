<script setup>
import { computed } from "vue";
import { Boxes, CheckSquare2, Server, SquareStack } from "lucide-vue-next";

const props = defineProps({
  modelValue: { type: Array, default: () => [] },
  nodes: { type: Array, default: () => [] },
  disabled: { type: Boolean, default: false },
});
const emit = defineEmits(["update:modelValue"]);

const treeNodes = computed(() => props.nodes.map((content) => {
  const instances = content.instances || [];
  return {
    node: content.node,
    servers: (content.servers || []).map((server) => ({
      ...server,
      instances: instances.filter((instance) => instance.server_id === server.server_id),
    })).filter((server) => server.instances.length),
  };
}).filter((content) => content.servers.length));

const selectedKeys = computed(() => new Set(
  props.modelValue.map((target) => target.node_id + ":" + target.instance_id),
));

function targetOf(content, server, instance) {
  return {
    node_id: content.node.id,
    node_name: content.node.name,
    server_id: server.server_id,
    instance_id: instance.instance_id,
    instance_name: instance.name || instance.instance_id,
  };
}

function nodeTargets(content) {
  return content.servers.flatMap((server) => (
    server.instances.map((instance) => targetOf(content, server, instance))
  ));
}

function serverTargets(content, server) {
  return server.instances.map((instance) => targetOf(content, server, instance));
}

function targetSelected(target) {
  return selectedKeys.value.has(target.node_id + ":" + target.instance_id);
}

function selectionState(targets) {
  const selected = targets.filter(targetSelected).length;
  return {
    checked: targets.length > 0 && selected === targets.length,
    indeterminate: selected > 0 && selected < targets.length,
  };
}

function replaceTargets(scope, enabled) {
  const keys = new Set(scope.map((target) => target.node_id + ":" + target.instance_id));
  const next = props.modelValue.filter((target) => (
    !keys.has(target.node_id + ":" + target.instance_id)
  ));
  if (enabled) next.push(...scope);
  emit("update:modelValue", next);
}

function setInstance(target, enabled) {
  replaceTargets([target], enabled);
}
</script>

<template>
  <div class="instance-selection">
    <div class="instance-selection-summary">
      <span><CheckSquare2 :size="14" />已选择 {{ modelValue.length }} 个实例</span>
      <el-button
        v-if="modelValue.length"
        text
        size="small"
        :disabled="disabled"
        @click="emit('update:modelValue', [])"
      >
        清空
      </el-button>
    </div>

    <div class="instance-selection-tree">
      <section v-for="content in treeNodes" :key="content.node.id" class="instance-node">
        <div class="instance-node-head">
          <el-checkbox
            :model-value="selectionState(nodeTargets(content)).checked"
            :indeterminate="selectionState(nodeTargets(content)).indeterminate"
            :disabled="disabled"
            @change="replaceTargets(nodeTargets(content), $event)"
          >
            <span class="instance-tree-name"><Boxes :size="15" />{{ content.node.name }}</span>
          </el-checkbox>
          <small>{{ nodeTargets(content).length }} 个实例</small>
        </div>

        <div v-for="server in content.servers" :key="server.server_id" class="instance-server">
          <div class="instance-server-head">
            <el-checkbox
              :model-value="selectionState(serverTargets(content, server)).checked"
              :indeterminate="selectionState(serverTargets(content, server)).indeterminate"
              :disabled="disabled"
              @change="replaceTargets(serverTargets(content, server), $event)"
            >
              <span class="instance-tree-name"><Server :size="14" />{{ server.name }}</span>
            </el-checkbox>
            <small>{{ server.server_id }} · {{ server.instances.length }} 个</small>
          </div>
          <div class="instance-leaf-list">
            <el-checkbox
              v-for="instance in server.instances"
              :key="instance.instance_id"
              :model-value="targetSelected(targetOf(content, server, instance))"
              :disabled="disabled"
              @change="setInstance(targetOf(content, server, instance), $event)"
            >
              <span class="instance-leaf">
                <span class="instance-tree-name">
                  <SquareStack :size="13" />{{ instance.name || instance.instance_id }}
                </span>
                <small>{{ instance.instance_id }} · {{ instance.state || "unknown" }}</small>
              </span>
            </el-checkbox>
          </div>
        </div>
      </section>
      <div v-if="!treeNodes.length" class="instance-selection-empty">当前没有可选实例</div>
    </div>
  </div>
</template>

<style scoped>
.instance-selection { border: 1px solid var(--el-border-color); border-radius: 5px; overflow: hidden; }
.instance-selection-summary {
  display: flex;
  min-height: 38px;
  padding: 0 10px;
  align-items: center;
  justify-content: space-between;
  background: #f7f9f8;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.instance-selection-summary span, .instance-tree-name {
  display: inline-flex;
  align-items: center;
  gap: 7px;
}
.instance-selection-summary span { color: #526057; font-size: 11px; font-weight: 600; }
.instance-selection-tree { max-height: 340px; overflow: auto; }
.instance-node + .instance-node { border-top: 1px solid var(--el-border-color); }
.instance-node-head, .instance-server-head {
  display: flex;
  min-height: 42px;
  padding: 0 12px;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
}
.instance-node-head { background: #f2f5f3; }
.instance-node-head small, .instance-server-head small, .instance-leaf small {
  overflow: hidden;
  color: var(--el-text-color-secondary);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.instance-server { border-top: 1px solid var(--el-border-color-extra-light); }
.instance-server-head { padding-left: 34px; background: #fbfcfb; }
.instance-leaf-list { display: grid; }
.instance-leaf-list :deep(.el-checkbox) {
  width: 100%;
  min-height: 38px;
  margin: 0;
  padding: 0 14px 0 58px;
  border-top: 1px solid var(--el-border-color-extra-light);
}
.instance-leaf-list :deep(.el-checkbox__label) { width: 100%; min-width: 0; }
.instance-leaf {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
}
.instance-tree-name { min-width: 0; }
.instance-selection-empty {
  padding: 34px 16px;
  color: var(--el-text-color-secondary);
  text-align: center;
  font-size: 12px;
}
</style>
