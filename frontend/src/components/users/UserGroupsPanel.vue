<script setup>
import { computed, onMounted, reactive, ref } from "vue";
import { Edit3, Plus, RefreshCw, ShieldCheck, Trash2, UsersRound } from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { request } from "../../api";
import PermissionMatrix from "./PermissionMatrix.vue";

const loading = ref(false);
const submitting = ref(false);
const groups = ref([]);
const permissions = ref([]);
const dialogOpen = ref(false);
const editing = ref(null);
const formRef = ref();
const form = reactive({ name: "", description: "", permissions: [] });
const rules = {
  name: [{ required: true, message: "请输入用户组名称", trigger: "blur" }],
};
const lockedCodes = computed(() =>
  editing.value?.code === "super_admin" ? permissions.value.map((item) => item.code) : ["permission.manage"],
);

async function load() {
  loading.value = true;
  try {
    const [groupData, permissionData] = await Promise.all([
      request("/api/v1/user-groups"),
      request("/api/v1/permissions"),
    ]);
    groups.value = groupData.items;
    permissions.value = permissionData.items;
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    loading.value = false;
  }
}

function openCreate() {
  editing.value = null;
  Object.assign(form, { name: "", description: "", permissions: [] });
  dialogOpen.value = true;
}

function openEdit(group) {
  editing.value = group;
  Object.assign(form, {
    name: group.name,
    description: group.description,
    permissions: [...group.permissions],
  });
  dialogOpen.value = true;
}

async function save() {
  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) return;
  submitting.value = true;
  try {
    const path = editing.value ? `/api/v1/user-groups/${editing.value.code}` : "/api/v1/user-groups";
    await request(path, {
      method: editing.value ? "PUT" : "POST",
      body: JSON.stringify(form),
    });
    dialogOpen.value = false;
    ElMessage.success(editing.value ? "用户组已更新" : "用户组已创建");
    await load();
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

async function remove(group) {
  try {
    await ElMessageBox.confirm(
      `删除用户组“${group.name}”？`,
      "删除用户组",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" },
    );
    await request(`/api/v1/user-groups/${group.code}`, { method: "DELETE" });
    ElMessage.success("用户组已删除");
    await load();
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message);
  }
}

onMounted(load);
</script>

<template>
  <div class="panel-stack">
    <div class="table-toolbar">
      <el-tooltip content="刷新">
        <el-button class="square-button" :loading="loading" aria-label="刷新" @click="load">
          <RefreshCw v-if="!loading" :size="16" />
        </el-button>
      </el-tooltip>
      <el-button type="primary" class="toolbar-primary" @click="openCreate">
        <Plus :size="16" />新增用户组
      </el-button>
    </div>
    <div class="table-frame">
      <el-table v-loading="loading" :data="groups" row-key="code">
        <el-table-column label="用户组" min-width="210">
          <template #default="{ row }">
            <div class="group-name">
              <ShieldCheck :size="16" />
              <div><strong>{{ row.name }}</strong><small>{{ row.code }}</small></div>
              <el-tag v-if="row.built_in" effect="plain" size="small">内置</el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="description" label="说明" min-width="280" />
        <el-table-column label="权限节点" width="100">
          <template #default="{ row }">{{ row.permissions.length }}</template>
        </el-table-column>
        <el-table-column label="用户" width="90">
          <template #default="{ row }">
            <span class="count-inline"><UsersRound :size="14" />{{ row.user_count }}</span>
          </template>
        </el-table-column>
        <el-table-column align="right" width="94">
          <template #default="{ row }">
            <el-tooltip v-if="row.code !== 'super_admin'" content="编辑用户组">
              <button class="table-action" type="button" aria-label="编辑用户组" @click="openEdit(row)">
                <Edit3 :size="16" />
              </button>
            </el-tooltip>
            <el-tooltip v-if="!row.built_in" content="删除用户组">
              <button
                class="table-action danger"
                type="button"
                aria-label="删除用户组"
                :disabled="row.user_count > 0"
                @click="remove(row)"
              >
                <Trash2 :size="16" />
              </button>
            </el-tooltip>
          </template>
        </el-table-column>
      </el-table>
    </div>
  </div>

  <el-dialog
    v-model="dialogOpen"
    :title="editing ? '编辑用户组' : '新增用户组'"
    width="820px"
  >
    <el-form ref="formRef" :model="form" :rules="rules" label-position="top">
      <div class="dialog-form-grid">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" :disabled="editing?.built_in" maxlength="100" />
        </el-form-item>
        <el-form-item label="说明">
          <el-input v-model="form.description" :disabled="editing?.built_in" maxlength="500" />
        </el-form-item>
      </div>
      <el-form-item label="权限节点">
        <PermissionMatrix
          v-model="form.permissions"
          :items="permissions"
          :disabled="editing?.code === 'super_admin'"
          :disabled-codes="lockedCodes"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="dialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>
