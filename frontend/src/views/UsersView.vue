<script setup>
import { computed, ref } from "vue";
import UserListPanel from "../components/users/UserListPanel.vue";
import UserGroupsPanel from "../components/users/UserGroupsPanel.vue";
import AuditPanel from "../components/users/AuditPanel.vue";
import { hasPermission, sessionState } from "../session";

const activeTab = ref("users");
const isSuperAdmin = computed(() => sessionState.user?.group_code === "super_admin");
const canViewAudit = computed(() => hasPermission("audit.view"));
</script>

<template>
  <div class="content-stack">
    <div class="page-toolbar">
      <div><h2>用户与权限</h2><p>账户、用户组、全局权限与操作审计</p></div>
    </div>
    <el-tabs v-model="activeTab" class="management-tabs">
      <el-tab-pane label="用户" name="users"><UserListPanel /></el-tab-pane>
      <el-tab-pane v-if="isSuperAdmin" label="用户组" name="groups" lazy><UserGroupsPanel /></el-tab-pane>
      <el-tab-pane v-if="canViewAudit" label="操作日志" name="audit" lazy><AuditPanel /></el-tab-pane>
    </el-tabs>
  </div>
</template>
