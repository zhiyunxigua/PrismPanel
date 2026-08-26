// ServerDetailView load() 的纯逻辑：解析 /api/v1/servers?node_id=xxx 响应并分类结果。
// 独立成模块以便用 node:test 单测（不依赖 Vue / Element Plus）。
//
// 背景：修复「服务器不存在」误报。任意操作后都会触发 load()/load(true)，
// 而 daemon server.list 在节点刚重连/忙碌时可能暂时返回空列表；此时不能
// 把「列表为空/目标缺失」直接当成「服务器被删除」跳回列表页。

// 目标缺失时的最大重试次数（初始 1 次 + 重试 N 次）。
export const SERVER_LIST_RETRIES = 2;
// 每次重试的间隔毫秒数。
export const SERVER_LIST_RETRY_DELAY_MS = 300;

// resolveServerList(data, serverId)
// 返回分类结果：
//   { status: "ok", server, instances }   —— 找到目标服务器
//   { status: "node_error", message }      —— 响应携带节点错误（优先展示）
//   { status: "empty" }                    —— 列表为空且无节点错误（疑似节点未就绪/刚重连）
//   { status: "missing" }                  —— 列表非空但目标不在其中（服务器确实不存在）
export function resolveServerList(data, serverId) {
  if (!data || typeof data !== "object") {
    return { status: "node_error", message: "服务器返回了无效数据" };
  }
  if (data.error) {
    return { status: "node_error", message: data.error.message || "节点不可用" };
  }
  const servers = Array.isArray(data.servers) ? data.servers : [];
  const target = servers.find((item) => item && item.server_id === serverId);
  if (target) {
    const instances = Array.isArray(data.instances)
      ? data.instances.filter((item) => item && item.server_id === serverId)
      : [];
    return { status: "ok", server: target, instances };
  }
  if (servers.length === 0) {
    return { status: "empty" };
  }
  return { status: "missing" };
}

// sleep 供 load() 重试间隔使用；独立出来便于测试。
export function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
