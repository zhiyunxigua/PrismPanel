<script setup>
import { computed, onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  Activity,
  Bell,
  Boxes,
  CalendarClock,
  ChevronDown,
  Download,
  Gamepad2,
  Gauge,
  Inbox,
  ListTodo,
  Laptop,
  LogOut,
  Menu,
  Moon,
  Network,
  Package,
  Server,
  Settings,
  ShieldCheck,
  Sun,
  UserRound,
  Users,
  X,
} from "lucide-vue-next";
import { ElMessage } from "element-plus";
import { changePassword, hasPermission, logout, sessionState } from "../session";
import { checkWinAppUpdate, installWinAppUpdate, isWinApp } from "../runtime";
import { currentTheme, toggleTheme } from "../theme";
import TaskRunDrawer from "../components/TaskRunDrawer.vue";

const route = useRoute();
const router = useRouter();
const isDarkTheme = computed(() => currentTheme.value === "dark");
const mobileNavigationOpen = ref(false);
const passwordDialogOpen = ref(false);
const taskDrawerOpen = ref(false);
const taskRunningCount = ref(0);
const alertDrawerOpen = ref(false);
const passwordSubmitting = ref(false);
const winAppUpdateOpen = ref(false);
const winAppUpdateInstalling = ref(false);
const winAppUpdateRelease = ref(null);
const passwordFormRef = ref();
const passwordForm = ref({
  currentPassword: "",
  newPassword: "",
  confirmPassword: "",
});

onMounted(checkForWinAppUpdate);

async function checkForWinAppUpdate() {
  if (!isWinApp()) return;
  try {
    const status = await checkWinAppUpdate();
    const release = status?.update_available ? status.latest : null;
    if (!release || window.sessionStorage.getItem("prism:winapp-update-dismissed") === release.version) return;
    winAppUpdateRelease.value = release;
    winAppUpdateOpen.value = true;
  } catch {
    // Update checks must not block normal startup when the Panel is offline.
  }
}

function postponeWinAppUpdate() {
  if (winAppUpdateRelease.value?.version) {
    window.sessionStorage.setItem("prism:winapp-update-dismissed", winAppUpdateRelease.value.version);
  }
  winAppUpdateOpen.value = false;
}

async function installAvailableWinAppUpdate() {
  if (!winAppUpdateRelease.value || winAppUpdateInstalling.value) return;
  winAppUpdateInstalling.value = true;
  try {
    await installWinAppUpdate(winAppUpdateRelease.value.version);
  } catch (error) {
    winAppUpdateInstalling.value = false;
    ElMessage.error(error.message || "WinApp 更新失败");
  }
}

const navigation = computed(() => [
  { label: "总览", route: "overview", icon: Gauge },
  { label: "加入游戏", route: "join-game", icon: Gamepad2, winAppOnly: true },
  { label: "网络游戏", route: "net-games", icon: Activity, permission: "dashboard.view" },
  { label: "服务器", route: "servers", icon: Server, permission: "server.view" },
  { label: "定时任务", route: "scheduled-tasks", icon: CalendarClock, permission: "schedule.view" },
  { label: "插件", route: "plugins", icon: Package, permission: "plugin.view" },
  { label: "用户", route: "users", icon: Users, permission: "user.view" },
  { label: "网络白名单", route: "firewall", icon: ShieldCheck, permission: "firewall.view" },
  { label: "节点", route: "nodes", icon: Network, permission: "node.view" },
  { label: "客户端更新", route: "winapp-updates", icon: Laptop, superAdmin: true },
].filter((item) => (!item.winAppOnly || isWinApp())
  && (!item.permission || hasPermission(item.permission))
  && (!item.superAdmin || sessionState.user?.group_code === "super_admin")));

const passwordRules = {
  currentPassword: [{ required: true, message: "请输入当前密码", trigger: "blur" }],
  newPassword: [
    { required: true, message: "请输入新密码", trigger: "blur" },
    {
      validator: (_rule, value, callback) => {
        if (Array.from(value || "").length < 6) callback(new Error("密码至少需要 6 个字符"));
        else callback();
      },
      trigger: "blur",
    },
  ],
  confirmPassword: [
    {
      validator: (_rule, value, callback) => {
        if (!value) callback(new Error("请再次输入新密码"));
        else if (value !== passwordForm.value.newPassword) callback(new Error("两次密码不一致"));
        else callback();
      },
      trigger: "blur",
    },
  ],
};

async function signOut() {
  await logout();
  await router.replace({ name: "login" });
}

function handleUserCommand(command) {
  if (command === "password") passwordDialogOpen.value = true;
  if (command === "panel") router.push({ name: "panel-setup" });
  if (command === "logout") signOut();
}

async function submitPassword() {
  const valid = await passwordFormRef.value?.validate().catch(() => false);
  if (!valid) return;
  passwordSubmitting.value = true;
  try {
    const result = await changePassword(passwordForm.value.currentPassword, passwordForm.value.newPassword);
    passwordDialogOpen.value = false;
    passwordForm.value = { currentPassword: "", newPassword: "", confirmPassword: "" };
    if (result?.warning) ElMessage.warning(result.warning);
    else ElMessage.success("密码已更新");
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    passwordSubmitting.value = false;
  }
}
</script>

<template>
  <div class="app-shell">
    <div
      v-if="mobileNavigationOpen"
      class="navigation-scrim"
      @click="mobileNavigationOpen = false"
    />
    <aside class="sidebar" :class="{ open: mobileNavigationOpen }">
      <div class="brand">
        <div class="brand-mark"><Boxes :size="19" /></div>
        <div>
          <strong>PrismPanel</strong>
          <span>控制面板</span>
        </div>
        <button
          class="icon-control sidebar-close"
          type="button"
          aria-label="关闭导航"
          @click="mobileNavigationOpen = false"
        >
          <X :size="18" />
        </button>
      </div>

      <nav class="primary-navigation" aria-label="主导航">
        <router-link
          v-for="item in navigation"
          :key="item.route"
          :to="{ name: item.route }"
          @click="mobileNavigationOpen = false"
        >
          <component :is="item.icon" :size="18" />
          <span>{{ item.label }}</span>
        </router-link>
      </nav>

      <div class="sidebar-status">
        <span class="status-indicator" />
        <div>
          <strong>面板服务</strong>
          <span>运行中</span>
        </div>
      </div>
    </aside>

    <div class="workspace">
      <header class="topbar">
        <div class="page-identity">
          <button
            class="icon-control mobile-menu"
            type="button"
            aria-label="打开导航"
            @click="mobileNavigationOpen = true"
          >
            <Menu :size="20" />
          </button>
          <div>
            <span class="breadcrumb">PrismPanel</span>
            <h1>{{ route.meta.title }}</h1>
          </div>
        </div>

        <div class="topbar-actions">
          <el-tooltip :content="isDarkTheme ? '切换为亮色主题' : '切换为暗色主题'" placement="bottom">
            <button
              class="icon-control theme-control"
              type="button"
              :aria-label="isDarkTheme ? '切换为亮色主题' : '切换为暗色主题'"
              :aria-pressed="isDarkTheme"
              @click="toggleTheme"
            >
              <Sun v-if="isDarkTheme" :size="18" />
              <Moon v-else :size="18" />
            </button>
          </el-tooltip>
          <el-tooltip v-if="hasPermission('task.view')" content="执行日志" placement="bottom">
            <button class="icon-control count-control" type="button" aria-label="执行日志" @click="taskDrawerOpen = true">
              <ListTodo :size="19" />
              <span>{{ taskRunningCount }}</span>
            </button>
          </el-tooltip>
          <el-tooltip content="告警" placement="bottom">
            <button class="icon-control count-control" type="button" aria-label="告警" @click="alertDrawerOpen = true">
              <Bell :size="19" />
              <span>0</span>
            </button>
          </el-tooltip>

          <el-dropdown trigger="click" @command="handleUserCommand">
            <button class="user-control" type="button">
              <span class="user-avatar"><UserRound :size="17" /></span>
              <span class="user-copy">
                <strong>{{ sessionState.user?.display_name }}</strong>
                <small>{{ sessionState.user?.group?.name }}</small>
              </span>
              <ChevronDown :size="15" />
            </button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="password">
                  <ShieldCheck :size="16" /> 修改密码
                </el-dropdown-item>
                <el-dropdown-item v-if="isWinApp()" command="panel">
                  <Settings :size="16" /> 面板地址
                </el-dropdown-item>
                <el-dropdown-item command="logout" divided>
                  <LogOut :size="16" /> 退出登录
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </header>

      <main class="page-content">
        <router-view />
      </main>
    </div>
  </div>

  <el-dialog v-model="passwordDialogOpen" title="修改密码" width="430px">
    <el-form
      ref="passwordFormRef"
      :model="passwordForm"
      :rules="passwordRules"
      label-position="top"
      @submit.prevent="submitPassword"
    >
      <el-form-item label="当前密码" prop="currentPassword">
        <el-input v-model="passwordForm.currentPassword" type="password" show-password />
      </el-form-item>
      <el-form-item label="新密码" prop="newPassword">
        <el-input v-model="passwordForm.newPassword" type="password" show-password />
      </el-form-item>
      <el-form-item label="确认新密码" prop="confirmPassword">
        <el-input v-model="passwordForm.confirmPassword" type="password" show-password />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="passwordDialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="passwordSubmitting" @click="submitPassword">
        保存
      </el-button>
    </template>
  </el-dialog>

  <TaskRunDrawer
    v-if="hasPermission('task.view')"
    v-model="taskDrawerOpen"
    @running-count="taskRunningCount = $event"
  />
  <el-drawer v-model="alertDrawerOpen" title="告警" size="min(420px, 92vw)">
    <div class="drawer-empty"><Inbox :size="24" /><span>暂无未处理告警</span></div>
  </el-drawer>

  <el-dialog
    v-model="winAppUpdateOpen"
    title="发现 WinApp 新版本"
    width="min(520px, 94vw)"
    :show-close="false"
    :close-on-click-modal="false"
    :close-on-press-escape="false"
  >
    <div v-if="winAppUpdateRelease" class="winapp-update-dialog">
      <div class="winapp-update-version">
        <Download :size="20" />
        <div><strong>{{ winAppUpdateRelease.version }}</strong><span>{{ (winAppUpdateRelease.size / 1024 / 1024).toFixed(2) }} MiB</span></div>
      </div>
      <div class="winapp-update-notes">{{ winAppUpdateRelease.notes || "本次版本未填写更新日志。" }}</div>
    </div>
    <template #footer>
      <el-button :disabled="winAppUpdateInstalling" @click="postponeWinAppUpdate">下次启动时再提醒我</el-button>
      <el-button type="primary" :loading="winAppUpdateInstalling" @click="installAvailableWinAppUpdate">
        立即更新
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.winapp-update-dialog { display: grid; gap: 14px; }
.winapp-update-version { display: flex; align-items: center; gap: 10px; color: #2563a8; }
.winapp-update-version strong, .winapp-update-version span { display: block; }
.winapp-update-version strong { font-size: 18px; }
.winapp-update-version span { margin-top: 2px; color: #6e7871; font-size: 12px; }
.winapp-update-notes { max-height: 240px; overflow: auto; padding: 12px; color: #3d4941; background: #f5f7f5; border: 1px solid #dce2dd; border-radius: 6px; white-space: pre-wrap; word-break: break-word; }
:global(html.dark) .winapp-update-notes { color: #d5dcd7; background: #222824; border-color: #3b453e; }
</style>
