import { createRouter, createWebHashHistory } from "vue-router";
import { ensureSession, sessionState } from "./session";
import { isWinApp, runtimeConfig } from "./runtime";

const AppLayout = () => import("./layouts/AppLayout.vue");
const LoginView = () => import("./views/LoginView.vue");
const PanelSetupView = () => import("./views/PanelSetupView.vue");
const OverviewView = () => import("./views/OverviewView.vue");
const JoinGameView = () => import("./views/JoinGameView.vue");
const UsersView = () => import("./views/UsersView.vue");
const NodesView = () => import("./views/NodesView.vue");
const NodeDetailView = () => import("./views/NodeDetailView.vue");
const ServersView = () => import("./views/ServersView.vue");
const ServerDetailView = () => import("./views/ServerDetailView.vue");
const ScheduledTasksView = () => import("./views/ScheduledTasksView.vue");
const PluginsView = () => import("./views/PluginsView.vue");
const NetGamesView = () => import("./views/NetGamesView.vue");
const FirewallView = () => import("./views/FirewallView.vue");
const WinAppUpdatesView = () => import("./views/WinAppUpdatesView.vue");
const SettingsView = () => import("./views/SettingsView.vue");

const routes = [
  {
    path: "/panel-setup",
    name: "panel-setup",
    component: PanelSetupView,
    meta: { guest: true, desktopSetup: true, title: "面板连接" },
  },
  {
    path: "/login",
    name: "login",
    component: LoginView,
    meta: { guest: true, title: "登录" },
  },
  {
    path: "/",
    component: AppLayout,
    meta: { auth: true },
    children: [
      { path: "", name: "overview", component: OverviewView, meta: { title: "总览" } },
      {
        path: "join-game",
        name: "join-game",
        component: JoinGameView,
        meta: { title: "加入游戏", winAppOnly: true },
      },
      {
        path: "settings",
        name: "settings",
        component: SettingsView,
        meta: { title: "总设置", winAppOnly: true },
      },
      {
        path: "servers",
        name: "servers",
        component: ServersView,
        meta: { title: "服务器", permission: "server.view" },
      },
      {
        path: "servers/:nodeId/:serverId",
        name: "server-detail",
        component: ServerDetailView,
        meta: { title: "服务器详情", permission: "server.view" },
      },
      {
        path: "scheduled-tasks",
        name: "scheduled-tasks",
        component: ScheduledTasksView,
        meta: { title: "定时任务", permission: "schedule.view" },
      },
      {
        path: "plugins",
        name: "plugins",
        component: PluginsView,
        meta: { title: "插件", permission: "plugin.view" },
      },
      {
        path: "net-games",
        name: "net-games",
        component: NetGamesView,
        meta: { title: "服务器监控", permission: "dashboard.view" },
      },
      {
        path: "users",
        name: "users",
        component: UsersView,
        meta: { title: "用户", permission: "user.view" },
      },
      {
        path: "firewall",
        name: "firewall",
        component: FirewallView,
        meta: { title: "网络白名单", permission: "firewall.view" },
      },
      {
        path: "nodes",
        name: "nodes",
        component: NodesView,
        meta: { title: "节点", permission: "node.view" },
      },
      {
        path: "nodes/:id",
        name: "node-detail",
        component: NodeDetailView,
        meta: { title: "节点详情", permission: "node.view" },
      },
      {
        path: "winapp-updates",
        name: "winapp-updates",
        component: WinAppUpdatesView,
        meta: { title: "客户端更新", superAdmin: true },
      },
    ],
  },
  { path: "/:pathMatch(.*)*", redirect: "/" },
];

const router = createRouter({
  history: createWebHashHistory(),
  routes,
});

router.beforeEach(async (to) => {
  if (isWinApp() && !runtimeConfig.configured && to.name !== "panel-setup") {
    return { name: "panel-setup" };
  }
  if (to.name === "panel-setup") {
    if (!isWinApp()) return { name: sessionState.user ? "overview" : "login" };
    return true;
  }
  await ensureSession();
  if (to.meta.guest && sessionState.user) return { name: "overview" };
  if (!to.meta.guest && !sessionState.user) {
    return { name: "login", query: { redirect: to.fullPath } };
  }
  if (to.meta.winAppOnly && !isWinApp()) return { name: "overview" };
  if (to.meta.superAdmin && sessionState.user?.group_code !== "super_admin") return { name: "overview" };
  if (to.meta.permission) {
    const permissions = sessionState.user?.permissions || [];
    if (!permissions.includes("*") && !permissions.includes(to.meta.permission)) {
      return { name: "overview" };
    }
  }
  return true;
});

export default router;
