<script setup>
import { computed, onMounted, reactive, ref } from "vue";
import {
  KeyRound,
  MoreHorizontal,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  ShieldOff,
  Trash2,
  UserCog,
  UsersRound,
} from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { request } from "../../api";
import { hasPermission, sessionState } from "../../session";
import PermissionMatrix from "./PermissionMatrix.vue";

const loading = ref(false);
const submitting = ref(false);
const users = ref([]);
const groups = ref([]);
const total = ref(0);
const filters = reactive({ search: "", status: "", page: 1, pageSize: 20 });
const dialogOpen = ref(false);
const editing = ref(null);
const formRef = ref();
const form = reactive({
  username: "",
  display_name: "",
  group_code: "observer",
  status: "active",
  password: "",
});
const permissionDialogOpen = ref(false);
const permissionLoading = ref(false);
const permissionSubmitting = ref(false);
const permissionUser = ref(null);
const permissionProfile = ref(null);
const selectedPermissions = ref([]);

const isSuperAdmin = computed(() => sessionState.user?.group_code === "super_admin");
const canCreate = computed(() => hasPermission("user.create"));
const groupTypes = { super_admin: "danger", admin: "warning", operator: "primary", observer: "info" };
const assignableGroups = computed(() => {
  if (isSuperAdmin.value) return groups.value;
  return groups.value.filter((group) =>
    group.code !== "super_admin" &&
    permissionSetContains(sessionState.user?.permissions || [], group.permissions),
  );
});
const permissionItems = computed(() => permissionProfile.value?.permissions || []);
const permissionReadOnly = computed(() => permissionUser.value?.group_code === "super_admin");
const rules = {
  username: [
    { required: true, message: "请输入用户名", trigger: "blur" },
    {
      pattern: /^[a-z0-9][a-z0-9._-]{2,31}$/,
      message: "使用 3-32 位小写字母、数字、点、下划线或连字符",
      trigger: "blur",
    },
  ],
  display_name: [{ required: true, message: "请输入显示名称", trigger: "blur" }],
  group_code: [{ required: true, message: "请选择用户组", trigger: "change" }],
  password: [{
    validator: (_rule, value, callback) => {
      if (!editing.value && !value) callback(new Error("请输入初始密码"));
      else if (value && Array.from(value).length < 6) callback(new Error("密码至少需要 6 个字符"));
      else callback();
    },
    trigger: "blur",
  }],
};

async function load() {
  loading.value = true;
  const query = new URLSearchParams({
    page: String(filters.page),
    page_size: String(filters.pageSize),
  });
  if (filters.search) query.set("search", filters.search);
  if (filters.status) query.set("status", filters.status);
  try {
    const [userData, groupData] = await Promise.all([
      request(`/api/v1/users?${query}`),
      request("/api/v1/user-groups"),
    ]);
    users.value = userData.items;
    total.value = userData.total;
    groups.value = groupData.items;
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    loading.value = false;
  }
}

function search() {
  filters.page = 1;
  load();
}

function openCreate() {
  editing.value = null;
  const defaultGroup = assignableGroups.value.find((item) => item.code === "observer")
    || assignableGroups.value[0];
  Object.assign(form, {
    username: "",
    display_name: "",
    group_code: defaultGroup?.code || "",
    status: "active",
    password: "",
  });
  dialogOpen.value = true;
}

function openEdit(user) {
  editing.value = user;
  Object.assign(form, {
    username: user.username,
    display_name: user.display_name,
    group_code: user.group_code,
    status: user.status,
    password: "",
  });
  dialogOpen.value = true;
}

async function save() {
  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) return;
  submitting.value = true;
  try {
    if (editing.value) {
      const updated = await request(`/api/v1/users/${editing.value.id}`, {
        method: "PUT",
        body: JSON.stringify({
          display_name: form.display_name,
          group_code: form.group_code,
          status: form.status,
        }),
      });
      if (updated.id === sessionState.user.id) sessionState.user = updated;
      ElMessage.success("用户信息已更新");
    } else {
      await request("/api/v1/users", {
        method: "POST",
        body: JSON.stringify({
          username: form.username,
          display_name: form.display_name,
          group_code: form.group_code,
          password: form.password,
        }),
      });
      ElMessage.success("用户已创建");
    }
    dialogOpen.value = false;
    await load();
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

async function resetPassword(user) {
  try {
    const result = await ElMessageBox.prompt(`为“${user.display_name}”设置新密码`, "重置密码", {
      inputType: "password",
      inputPlaceholder: "至少 6 个字符",
	  inputValidator: (value) => Array.from(value || "").length >= 6 || "密码至少需要 6 个字符",
      confirmButtonText: "重置",
      cancelButtonText: "取消",
    });
    await request(`/api/v1/users/${user.id}/reset-password`, {
      method: "POST",
      body: JSON.stringify({ password: result.value }),
    });
    ElMessage.success("密码已重置，现有会话已撤销");
    await load();
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message);
  }
}

async function revokeSessions(user) {
  try {
    await ElMessageBox.confirm(`撤销“${user.display_name}”的全部登录会话？`, "撤销会话", {
      type: "warning",
      confirmButtonText: "撤销",
      cancelButtonText: "取消",
    });
    await request(`/api/v1/users/${user.id}/revoke-sessions`, {
      method: "POST",
      body: "{}",
    });
    ElMessage.success("会话已撤销");
    await load();
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message);
  }
}

async function remove(user) {
  try {
    const result = await ElMessageBox.prompt(`删除“${user.display_name}”并撤销其登录会话。`, "删除用户", {
      type: "error",
      inputPlaceholder: `输入 ${user.username} 确认`,
      inputValidator: (value) => value === user.username || "用户名不匹配",
      confirmButtonText: "删除",
      cancelButtonText: "取消",
    });
    if (result.value !== user.username) return;
    await request(`/api/v1/users/${user.id}`, { method: "DELETE" });
    ElMessage.success("用户已删除");
    await load();
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message);
  }
}

async function openPermissions(user) {
  permissionUser.value = user;
  permissionProfile.value = null;
  selectedPermissions.value = [];
  permissionDialogOpen.value = true;
  permissionLoading.value = true;
  try {
    permissionProfile.value = await request(`/api/v1/users/${user.id}/permissions`);
    selectedPermissions.value = permissionProfile.value.permissions
      .filter((item) => item.effective)
      .map((item) => item.code);
  } catch (error) {
    permissionDialogOpen.value = false;
    ElMessage.error(error.message);
  } finally {
    permissionLoading.value = false;
  }
}

function restoreGroupDefaults() {
  selectedPermissions.value = permissionItems.value
    .filter((item) => item.group_value)
    .map((item) => item.code);
}

async function savePermissions() {
  permissionSubmitting.value = true;
  try {
    permissionProfile.value = await request(
      `/api/v1/users/${permissionUser.value.id}/permissions`,
      {
        method: "PUT",
        body: JSON.stringify({ permissions: selectedPermissions.value }),
      },
    );
    selectedPermissions.value = permissionProfile.value.permissions
      .filter((item) => item.effective)
      .map((item) => item.code);
    permissionDialogOpen.value = false;
    ElMessage.success("个人权限已更新");
    await load();
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    permissionSubmitting.value = false;
  }
}

function command(action, user) {
  if (action === "edit") openEdit(user);
  if (action === "permissions") openPermissions(user);
  if (action === "reset") resetPassword(user);
  if (action === "revoke") revokeSessions(user);
  if (action === "delete") remove(user);
}

function canManage(user, permission) {
  if (!hasPermission(permission)) return false;
  if (isSuperAdmin.value) return true;
  if (user.group_code === "super_admin") return false;
  return permissionSetContains(sessionState.user?.permissions || [], user.permissions || []);
}

function rowHasActions(user) {
  return canManage(user, "user.update")
    || canManage(user, "user.password.reset")
    || canManage(user, "user.sessions.revoke")
    || (canManage(user, "user.delete") && user.id !== sessionState.user.id)
    || isSuperAdmin.value;
}

function permissionSetContains(available, required) {
  if (available.includes("*")) return true;
  const set = new Set(available);
  return required.every((permission) => set.has(permission));
}

function formatDate(value) {
  if (!value) return "从未";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

onMounted(load);
</script>

<template>
  <div class="panel-stack">
    <div class="table-toolbar">
      <el-input
        v-model="filters.search"
        class="search-input"
        placeholder="搜索用户名或显示名称"
        clearable
        @keyup.enter="search"
        @clear="search"
      >
        <template #prefix><Search :size="16" /></template>
      </el-input>
      <el-select
        v-model="filters.status"
        class="status-filter"
        placeholder="全部状态"
        clearable
        @change="search"
      >
        <el-option label="正常" value="active" />
        <el-option label="已禁用" value="disabled" />
      </el-select>
      <el-tooltip content="刷新">
        <el-button class="square-button" :loading="loading" aria-label="刷新" @click="load">
          <RefreshCw v-if="!loading" :size="16" />
        </el-button>
      </el-tooltip>
      <el-button v-if="canCreate" type="primary" class="toolbar-primary" @click="openCreate">
        <Plus :size="16" />创建用户
      </el-button>
    </div>
    <div class="table-frame">
      <el-table v-loading="loading" :data="users" row-key="id">
        <el-table-column label="用户" min-width="210">
          <template #default="{ row }">
            <div class="user-cell">
              <span class="table-avatar">{{ row.display_name.slice(0, 1).toUpperCase() }}</span>
              <div><strong>{{ row.display_name }}</strong><small>{{ row.username }}</small></div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="用户组" min-width="150">
          <template #default="{ row }">
            <el-tag :type="groupTypes[row.group_code] || 'info'" effect="plain">
              {{ row.group.name }}
            </el-tag>
            <el-tag v-if="row.has_permission_overrides" class="inline-tag" type="warning" effect="plain">
              个人权限
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <span class="account-status" :class="row.status">
              {{ row.status === "active" ? "正常" : "已禁用" }}
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="active_sessions" label="会话" width="74" />
        <el-table-column label="最后登录" min-width="160">
          <template #default="{ row }">{{ formatDate(row.last_login_at) }}</template>
        </el-table-column>
        <el-table-column align="right" width="62">
          <template #default="{ row }">
            <el-dropdown v-if="rowHasActions(row)" trigger="click" @command="(action) => command(action, row)">
              <button class="table-action" type="button" :aria-label="`管理 ${row.username}`">
                <MoreHorizontal :size="18" />
              </button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item v-if="canManage(row, 'user.update')" command="edit">
                    <UserCog :size="16" />编辑
                  </el-dropdown-item>
                  <el-dropdown-item v-if="isSuperAdmin" command="permissions">
                    <ShieldCheck :size="16" />个人权限
                  </el-dropdown-item>
                  <el-dropdown-item v-if="canManage(row, 'user.password.reset')" command="reset">
                    <KeyRound :size="16" />重置密码
                  </el-dropdown-item>
                  <el-dropdown-item v-if="canManage(row, 'user.sessions.revoke')" command="revoke">
                    <ShieldOff :size="16" />撤销会话
                  </el-dropdown-item>
                  <el-dropdown-item
                    v-if="canManage(row, 'user.delete') && row.id !== sessionState.user.id"
                    command="delete"
                    divided
                  >
                    <Trash2 :size="16" />删除
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
        </el-table-column>
        <template #empty>
          <div class="table-empty"><UsersRound :size="24" /><span>暂无用户</span></div>
        </template>
      </el-table>
    </div>
    <el-pagination
      v-if="total > filters.pageSize"
      v-model:current-page="filters.page"
      class="table-pagination"
      layout="total, prev, pager, next"
      :page-size="filters.pageSize"
      :total="total"
      @current-change="load"
    />
  </div>

  <el-dialog v-model="dialogOpen" :title="editing ? '编辑用户' : '创建用户'" width="520px">
    <el-form ref="formRef" :model="form" :rules="rules" label-position="top">
      <div class="dialog-form-grid">
        <el-form-item label="用户名" prop="username">
          <el-input v-model="form.username" :disabled="Boolean(editing)" autocomplete="off" />
        </el-form-item>
        <el-form-item label="显示名称" prop="display_name">
          <el-input v-model="form.display_name" />
        </el-form-item>
      </div>
      <div class="dialog-form-grid">
        <el-form-item label="用户组" prop="group_code">
          <el-select v-model="form.group_code">
            <el-option
              v-for="group in assignableGroups"
              :key="group.code"
              :label="group.name"
              :value="group.code"
            />
          </el-select>
        </el-form-item>
        <el-form-item v-if="editing" label="状态" prop="status">
          <el-select v-model="form.status" :disabled="editing.id === sessionState.user.id">
            <el-option label="正常" value="active" />
            <el-option label="已禁用" value="disabled" />
          </el-select>
        </el-form-item>
      </div>
      <el-form-item v-if="!editing" label="初始密码" prop="password">
        <el-input
          v-model="form.password"
          type="password"
          show-password
          autocomplete="new-password"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="dialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="save">保存</el-button>
    </template>
  </el-dialog>

  <el-dialog
    v-model="permissionDialogOpen"
    :title="`${permissionUser?.display_name || '用户'} · 个人权限`"
    width="840px"
  >
    <div v-loading="permissionLoading">
      <PermissionMatrix
        v-if="permissionProfile"
        v-model="selectedPermissions"
        :items="permissionItems"
        :disabled="permissionReadOnly"
        :disabled-codes="['permission.manage']"
        show-inheritance
      />
    </div>
    <template #footer>
      <el-button @click="permissionDialogOpen = false">关闭</el-button>
      <el-button
        v-if="!permissionReadOnly"
        :disabled="permissionLoading"
        @click="restoreGroupDefaults"
      >
        恢复用户组默认
      </el-button>
      <el-button
        v-if="!permissionReadOnly"
        type="primary"
        :loading="permissionSubmitting"
        :disabled="permissionLoading"
        @click="savePermissions"
      >
        保存
      </el-button>
    </template>
  </el-dialog>
</template>
