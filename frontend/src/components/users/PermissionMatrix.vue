<script setup>
import { computed } from "vue";

const props = defineProps({
  items: { type: Array, default: () => [] },
  modelValue: { type: Array, default: () => [] },
  disabled: { type: Boolean, default: false },
  disabledCodes: { type: Array, default: () => [] },
  showInheritance: { type: Boolean, default: false },
});
const emit = defineEmits(["update:modelValue"]);

const selected = computed({
  get: () => props.modelValue,
  set: (value) => emit("update:modelValue", value),
});
const disabledSet = computed(() => new Set(props.disabledCodes));
const categories = computed(() => {
  const result = [];
  const index = new Map();
  for (const item of props.items) {
    if (!index.has(item.category)) {
      index.set(item.category, result.length);
      result.push({ name: item.category, items: [] });
    }
    result[index.get(item.category)].items.push(item);
  }
  return result;
});

function state(item) {
  if (!props.showInheritance) return null;
  const effective = selected.value.includes(item.code);
  if (effective === item.group_value) return { label: "继承用户组", type: "info" };
  return effective
    ? { label: "额外允许", type: "success" }
    : { label: "单独取消", type: "danger" };
}
</script>

<template>
  <el-checkbox-group v-model="selected" class="permission-matrix">
    <section v-for="category in categories" :key="category.name" class="permission-category">
      <h4>{{ category.name }}</h4>
      <div class="permission-options">
        <el-checkbox
          v-for="item in category.items"
          :key="item.code"
          :value="item.code"
          :disabled="disabled || disabledSet.has(item.code)"
        >
          <span class="permission-label">
            <span>{{ item.name }}</span>
            <code>{{ item.code }}</code>
          </span>
          <el-tag
            v-if="state(item)"
            class="permission-source"
            :type="state(item).type"
            effect="plain"
            size="small"
          >
            {{ state(item).label }}
          </el-tag>
        </el-checkbox>
      </div>
    </section>
  </el-checkbox-group>
</template>

<style scoped>
.permission-matrix {
  display: grid;
  max-height: min(58vh, 620px);
  overflow: auto;
  border-top: 1px solid #e2e7e4;
}
.permission-category {
  display: grid;
  grid-template-columns: 100px minmax(0, 1fr);
  gap: 14px;
  padding: 13px 2px;
  border-bottom: 1px solid #e7ebe8;
}
.permission-category h4 {
  margin: 4px 0 0;
  color: #4f5a53;
  font-size: 12px;
}
.permission-options {
  display: grid;
  grid-template-columns: repeat(2, minmax(220px, 1fr));
  gap: 7px 12px;
}
.permission-options :deep(.el-checkbox) {
  width: 100%;
  min-height: 36px;
  margin: 0;
}
.permission-options :deep(.el-checkbox__label) {
  display: flex;
  align-items: center;
  min-width: 0;
  width: 100%;
  gap: 8px;
}
.permission-label {
  display: grid;
  min-width: 0;
  line-height: 1.25;
}
.permission-label > span {
  overflow: hidden;
  text-overflow: ellipsis;
}
.permission-label code {
  margin-top: 3px;
  color: #879089;
  font-size: 9px;
}
.permission-source {
  flex: 0 0 auto;
  margin-left: auto;
}
@media (max-width: 720px) {
  .permission-category { grid-template-columns: 1fr; gap: 8px; }
  .permission-options { grid-template-columns: 1fr; }
}
</style>
