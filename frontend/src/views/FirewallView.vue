<script setup>
import { computed, onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  AlertTriangle,
  CheckCircle2,
  LocateFixed,
  Pencil,
  Plus,
  RefreshCw,
  ShieldCheck,
  Trash2,
  X,
} from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { request } from "../api";
import { hasPermission } from "../session";

const route = useRoute();
const router = useRouter();
const nodes = ref([]);
const nodeID = ref("");
const view = ref(null);
const loadError = ref("");
const loadingNodes = ref(false);
const loadingView = ref(false);
const submitting = ref(false);
const myIPLoading = ref(false);
const ruleDialogOpen = ref(false);
const systemDialogOpen = ref(false);
const editingRuleID = ref("");
const ruleForm = ref(emptyRule());
const systemForm = ref({
  enabled: false,
  controlSources: [""],
  grantTTLSeconds: 600,
});

const selectedNode = computed(() => nodes.value.find((item) => item.id === nodeID.value) || null);
const status = computed(() => view.value?.status || null);
const rules = computed(() => view.value?.rules || []);
const canManage = computed(() => hasPermission("firewall.manage"));
const mutationDisabled = computed(() => (
  submitting.value || !status.value?.supported || status.value?.drift
));
const statusType = computed(() => {
  if (!status.value?.supported) return "info";
  if (status.value?.state === "ERROR") return "danger";
  if (status.value?.drift) return "warning";
  if (status.value?.table_present) return "success";
  return "info";
});
const statusLabel = computed(() => ({
  UNSUPPORTED: "不支持",
  ERROR: "异常",
  DRIFT: "配置漂移",
  APPLIED: "已应用",
  READY: "就绪",
})[status.value?.state] || status.value?.state || "未知");

function emptyRule() {
  return {
    enabled: true,
    protocols: ["tcp"],
    ports: [{ from: 25565, to: 25565 }],
    sources: [""],
    note: "",
  };
}

async function loadNodes() {
  loadingNodes.value = true;
  try {
    const data = await request("/api/v1/firewall/nodes");
    nodes.value = data.items || [];
    const requested = String(route.query.node_id || "");
    if (requested && nodes.value.some((item) => item.id === requested)) nodeID.value = requested;
    else if (!nodes.value.some((item) => item.id === nodeID.value)) nodeID.value = nodes.value[0]?.id || "";
    if (nodeID.value) {
      await router.replace({ name: "firewall", query: { node_id: nodeID.value } });
      await loadView();
    } else {
      view.value = null;
    }
  } catch (error) {
    loadError.value = error.message;
    ElMessage.error(error.message);
  } finally {
    loadingNodes.value = false;
  }
}

async function changeNode() {
  await router.replace({ name: "firewall", query: { node_id: nodeID.value } });
  await loadView();
}

async function loadView(silent = false) {
  if (!nodeID.value) return;
  if (!silent) loadingView.value = true;
  loadError.value = "";
  try {
    view.value = await request("/api/v1/firewall/nodes/" + encodeURIComponent(nodeID.value));
  } catch (error) {
    view.value = null;
    loadError.value = error.message;
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) loadingView.value = false;
  }
}

function openCreateRule() {
  editingRuleID.value = "";
  ruleForm.value = emptyRule();
  ruleDialogOpen.value = true;
}

function openEditRule(rule) {
  editingRuleID.value = rule.id;
  ruleForm.value = {
    enabled: rule.enabled,
    protocols: [...rule.protocols],
    ports: rule.ports.map((item) => ({ from: item.from, to: item.to })),
    sources: [...rule.sources],
    note: rule.note || "",
  };
  ruleDialogOpen.value = true;
}

function addPort() {
  ruleForm.value.ports.push({ from: 25565, to: 25565 });
}

function removePort(index) {
  if (ruleForm.value.ports.length > 1) ruleForm.value.ports.splice(index, 1);
}

function addSource(target) {
  target.push("");
}

function removeSource(target, index) {
  if (target.length > 1) target.splice(index, 1);
}

async function addMyIP() {
  myIPLoading.value = true;
  try {
    const data = await request("/api/v1/auth/client-ip");
    const ip = String(data.ip || "").trim();
    if (!ip) throw new Error("无法获取客户端 IP");
    const existing = ruleForm.value.sources.map((item) => item.trim());
    if (!existing.includes(ip)) {
      const emptyIndex = existing.findIndex((item) => !item);
      if (emptyIndex >= 0) ruleForm.value.sources[emptyIndex] = ip;
      else ruleForm.value.sources.push(ip);
    }
    ElMessage.success("已填入当前客户端 IP");
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    myIPLoading.value = false;
  }
}

function normalizedRule() {
  const protocols = [...new Set(ruleForm.value.protocols)];
  if (!protocols.length) throw new Error("至少选择一个协议");
  const ports = ruleForm.value.ports.map((item) => ({
    from: Number(item.from),
    to: Number(item.to),
  }));
  if (!ports.length || ports.some((item) => (
    !Number.isInteger(item.from) || !Number.isInteger(item.to)
    || item.from < 1 || item.to > 65535 || item.to < item.from
  ))) {
    throw new Error("端口范围必须在 1 到 65535 之间");
  }
  const sources = ruleForm.value.sources.map((item) => item.trim()).filter(Boolean);
  if (!sources.length) throw new Error("至少填写一个来源 IP 或 CIDR");
  return {
    enabled: ruleForm.value.enabled,
    protocols,
    ports,
    sources,
    note: ruleForm.value.note.trim(),
  };
}

async function saveRule() {
  let rule;
  try {
    rule = normalizedRule();
  } catch (error) {
    ElMessage.warning(error.message);
    return;
  }
  submitting.value = true;
  try {
    const base = "/api/v1/firewall/nodes/" + encodeURIComponent(nodeID.value) + "/rules";
    const path = editingRuleID.value ? base + "/" + encodeURIComponent(editingRuleID.value) : base;
    view.value = await request(path, {
      method: editingRuleID.value ? "PUT" : "POST",
      body: JSON.stringify({ expected_revision: status.value.revision, rule }),
    });
    ruleDialogOpen.value = false;
    ElMessage.success(editingRuleID.value ? "规则已更新" : "规则已创建");
  } catch (error) {
    ElMessage.error(error.message);
    if (["FIREWALL_REVISION_CONFLICT", "FIREWALL_DRIFT"].includes(error.code)) await loadView(true);
  } finally {
    submitting.value = false;
  }
}

async function deleteRule(rule) {
  try {
    await ElMessageBox.confirm(
      "确认删除该网络白名单规则？",
      "删除规则",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" },
    );
  } catch {
    return;
  }
  submitting.value = true;
  try {
    const path = "/api/v1/firewall/nodes/" + encodeURIComponent(nodeID.value)
      + "/rules/" + encodeURIComponent(rule.id);
    view.value = await request(path, {
      method: "DELETE",
      body: JSON.stringify({ expected_revision: status.value.revision }),
    });
    ElMessage.success("规则已删除");
  } catch (error) {
    ElMessage.error(error.message);
    if (["FIREWALL_REVISION_CONFLICT", "FIREWALL_DRIFT"].includes(error.code)) await loadView(true);
  } finally {
    submitting.value = false;
  }
}

function openSystemDialog() {
  const system = status.value?.system || {};
  systemForm.value = {
    enabled: Boolean(system.enabled),
    controlSources: system.control_sources?.length ? [...system.control_sources] : [""],
    grantTTLSeconds: Number(system.grant_ttl_seconds) || 600,
  };
  systemDialogOpen.value = true;
}

async function saveSystem() {
  const controlSources = systemForm.value.controlSources.map((item) => item.trim()).filter(Boolean);
  if (systemForm.value.enabled && !controlSources.length) {
    ElMessage.warning("启用系统访问保护时至少保留一个 Panel 控制来源");
    return;
  }
  try {
    await ElMessageBox.confirm(
      systemForm.value.enabled
        ? "应用后，daemon 端口只接受控制来源和临时授权来源。"
        : "关闭后，Prism 将不再限制 daemon 管理端口的网络来源。",
      "确认系统访问设置",
      { type: "warning", confirmButtonText: "应用", cancelButtonText: "取消" },
    );
  } catch {
    return;
  }
  submitting.value = true;
  try {
    const path = "/api/v1/firewall/nodes/" + encodeURIComponent(nodeID.value) + "/system";
    view.value = await request(path, {
      method: "PUT",
      body: JSON.stringify({
        expected_revision: status.value.revision,
        system: {
          enabled: systemForm.value.enabled,
          control_sources: controlSources,
          grant_ttl_seconds: Number(systemForm.value.grantTTLSeconds),
          include_caller_source: true,
        },
      }),
    });
    systemDialogOpen.value = false;
    ElMessage.success("系统访问设置已应用");
  } catch (error) {
    ElMessage.error(error.message);
    if (["FIREWALL_REVISION_CONFLICT", "FIREWALL_DRIFT"].includes(error.code)) await loadView(true);
  } finally {
    submitting.value = false;
  }
}

function formatPorts(ports = []) {
  return ports.map((item) => item.from === item.to ? String(item.from) : item.from + "-" + item.to).join(", ");
}

function formatDate(value) {
  if (!value) return "从未";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  }).format(new Date(value));
}

onMounted(loadNodes);
</script>

<template>
  <div class="content-stack">
    <div class="page-toolbar firewall-toolbar">
      <div class="firewall-heading">
        <span class="node-symbol"><ShieldCheck :size="18" /></span>
        <div>
          <h2>网络白名单</h2>
          <p>{{ selectedNode?.name || "未选择节点" }}</p>
        </div>
      </div>
      <div class="toolbar-actions">
        <el-select
          v-model="nodeID"
          class="firewall-node-select"
          :loading="loadingNodes"
          placeholder="选择节点"
          @change="changeNode"
        >
          <el-option
            v-for="node in nodes"
            :key="node.id"
            :label="node.name"
            :value="node.id"
          >
            <span class="node-option"><span>{{ node.name }}</span><small>{{ node.status }}</small></span>
          </el-option>
        </el-select>
        <el-tooltip content="刷新">
          <el-button
            class="square-button"
            :loading="loadingView"
            aria-label="刷新"
            @click="loadView()"
          >
            <RefreshCw v-if="!loadingView" :size="16" />
          </el-button>
        </el-tooltip>
        <el-button
          v-if="canManage"
          type="primary"
          :disabled="mutationDisabled"
          @click="openCreateRule"
        >
          <Plus :size="16" />新增规则
        </el-button>
      </div>
    </div>

    <el-alert v-if="loadError" type="error" :closable="false" show-icon :title="loadError" />
    <el-alert
      v-else-if="status && !status.supported"
      type="info"
      :closable="false"
      show-icon
      :title="status.reason || '当前节点不支持网络白名单管理'"
    />
    <el-alert
      v-else-if="status?.drift"
      type="warning"
      :closable="false"
      show-icon
      title="检测到 PrismPanel nftables 表被外部修改，写入操作已暂停"
    />
    <el-alert
      v-else-if="status?.last_error"
      type="error"
      :closable="false"
      show-icon
      :title="status.last_error"
    />

    <div v-loading="loadingView" class="firewall-content">
      <div v-if="status" class="detail-summary firewall-summary">
        <div>
          <span>状态</span>
          <strong><el-tag :type="statusType" effect="plain">{{ statusLabel }}</el-tag></strong>
        </div>
        <div><span>平台</span><strong>{{ status.os }} / {{ status.architecture }}</strong></div>
        <div><span>修订号</span><strong>#{{ status.revision }}</strong></div>
        <div><span>临时授权</span><strong>{{ status.grant_count }}</strong></div>
      </div>

      <section v-if="status" class="data-section">
        <div class="section-title">
          <div><h3>系统访问保护</h3></div>
          <el-button
            v-if="canManage"
            :disabled="mutationDisabled"
            @click="openSystemDialog"
          >
            <ShieldCheck :size="15" />配置
          </el-button>
        </div>
        <el-descriptions :column="2" border class="detail-descriptions">
          <el-descriptions-item label="保护状态">
            <el-tag :type="status.system.enabled ? 'success' : 'info'" effect="plain">
              {{ status.system.enabled ? "已启用" : "未启用" }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="临时授权 TTL">
            {{ status.system.grant_ttl_seconds }} 秒
          </el-descriptions-item>
          <el-descriptions-item label="控制来源" :span="2">
            <div class="tag-list">
              <el-tag
                v-for="source in status.system.control_sources"
                :key="source"
                effect="plain"
              >{{ source }}</el-tag>
              <span v-if="!status.system.control_sources?.length" class="muted">无</span>
            </div>
          </el-descriptions-item>
          <el-descriptions-item label="最近应用">
            {{ formatDate(status.last_applied_at) }}
          </el-descriptions-item>
          <el-descriptions-item label="规则表">
            {{ status.table_present ? "inet prismpanel" : "未创建" }}
          </el-descriptions-item>
        </el-descriptions>
      </section>

      <section v-if="status" class="data-section">
        <div class="section-title">
          <div><h3>业务端口规则</h3></div>
          <span class="section-count">{{ rules.length }} 条</span>
        </div>
        <div class="firewall-mobile-rules">
          <article v-for="rule in rules" :key="rule.id" class="firewall-rule-card">
            <header>
              <div>
                <el-tag :type="rule.enabled ? 'success' : 'info'" effect="plain">
                  {{ rule.enabled ? "启用" : "停用" }}
                </el-tag>
                <strong>{{ rule.protocols.map((item) => item.toUpperCase()).join(" / ") }}</strong>
              </div>
              <div v-if="canManage" class="row-actions">
                <button
                  class="icon-control"
                  type="button"
                  aria-label="编辑"
                  :disabled="mutationDisabled"
                  @click="openEditRule(rule)"
                ><Pencil :size="15" /></button>
                <button
                  class="icon-control danger-icon"
                  type="button"
                  aria-label="删除"
                  :disabled="mutationDisabled"
                  @click="deleteRule(rule)"
                ><Trash2 :size="15" /></button>
              </div>
            </header>
            <dl>
              <dt>端口</dt><dd><code>{{ formatPorts(rule.ports) }}</code></dd>
              <dt>来源</dt>
              <dd><div class="tag-list"><el-tag v-for="source in rule.sources" :key="source" effect="plain">{{ source }}</el-tag></div></dd>
              <dt>备注</dt><dd>{{ rule.note || "-" }}</dd>
            </dl>
          </article>
          <div v-if="!rules.length" class="table-empty"><ShieldCheck :size="24" /><span>暂无业务端口规则</span></div>
        </div>
        <div class="table-frame firewall-rule-table">
          <el-table :data="rules" row-key="id">
            <el-table-column label="状态" width="88">
              <template #default="{ row }">
                <el-tag :type="row.enabled ? 'success' : 'info'" effect="plain">
                  {{ row.enabled ? "启用" : "停用" }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="协议" width="105">
              <template #default="{ row }">
                <strong>{{ row.protocols.map((item) => item.toUpperCase()).join(" / ") }}</strong>
              </template>
            </el-table-column>
            <el-table-column label="端口" min-width="150">
              <template #default="{ row }"><code>{{ formatPorts(row.ports) }}</code></template>
            </el-table-column>
            <el-table-column label="来源" min-width="250">
              <template #default="{ row }">
                <div class="tag-list">
                  <el-tag v-for="source in row.sources" :key="source" effect="plain">{{ source }}</el-tag>
                </div>
              </template>
            </el-table-column>
            <el-table-column label="备注" min-width="160">
              <template #default="{ row }">{{ row.note || "-" }}</template>
            </el-table-column>
            <el-table-column v-if="canManage" label="操作" width="92" align="right">
              <template #default="{ row }">
                <div class="row-actions">
                  <el-tooltip content="编辑">
                    <button
                      class="icon-control"
                      type="button"
                      aria-label="编辑"
                      :disabled="mutationDisabled"
                      @click="openEditRule(row)"
                    ><Pencil :size="15" /></button>
                  </el-tooltip>
                  <el-tooltip content="删除">
                    <button
                      class="icon-control danger-icon"
                      type="button"
                      aria-label="删除"
                      :disabled="mutationDisabled"
                      @click="deleteRule(row)"
                    ><Trash2 :size="15" /></button>
                  </el-tooltip>
                </div>
              </template>
            </el-table-column>
            <template #empty>
              <div class="table-empty"><ShieldCheck :size="24" /><span>暂无业务端口规则</span></div>
            </template>
          </el-table>
        </div>
      </section>
    </div>
  </div>

  <el-dialog
    v-model="ruleDialogOpen"
    :title="editingRuleID ? '编辑网络白名单规则' : '新增网络白名单规则'"
    width="680px"
  >
    <el-form label-position="top">
      <div class="dialog-form-grid">
        <el-form-item label="状态">
          <el-switch v-model="ruleForm.enabled" active-text="启用" inactive-text="停用" />
        </el-form-item>
        <el-form-item label="协议">
          <el-checkbox-group v-model="ruleForm.protocols">
            <el-checkbox-button value="tcp">TCP</el-checkbox-button>
            <el-checkbox-button value="udp">UDP</el-checkbox-button>
          </el-checkbox-group>
        </el-form-item>
      </div>
      <el-form-item label="端口">
        <div class="list-editor">
          <div v-for="(port, index) in ruleForm.ports" :key="index" class="port-row">
            <label class="port-input"><span>起始端口</span><el-input-number v-model="port.from" :min="1" :max="65535" controls-position="right" /></label>
            <span class="port-separator">至</span>
            <label class="port-input"><span>结束端口</span><el-input-number v-model="port.to" :min="1" :max="65535" controls-position="right" /></label>
            <el-tooltip content="移除">
              <button
                class="icon-control"
                type="button"
                aria-label="移除端口"
                :disabled="ruleForm.ports.length === 1"
                @click="removePort(index)"
              ><X :size="15" /></button>
            </el-tooltip>
          </div>
          <el-button plain @click="addPort"><Plus :size="14" />添加端口范围</el-button>
        </div>
      </el-form-item>
      <el-form-item label="来源 IP / CIDR">
        <div class="list-editor">
          <div v-for="(_source, index) in ruleForm.sources" :key="index" class="source-row">
            <el-input v-model="ruleForm.sources[index]" placeholder="203.0.113.10 或 203.0.113.0/24" />
            <el-tooltip content="移除">
              <button
                class="icon-control"
                type="button"
                aria-label="移除来源"
                :disabled="ruleForm.sources.length === 1"
                @click="removeSource(ruleForm.sources, index)"
              ><X :size="15" /></button>
            </el-tooltip>
          </div>
          <el-button plain :loading="myIPLoading" @click="addMyIP">
            <LocateFixed :size="14" />添加我的 IP
          </el-button>
          <el-button plain @click="addSource(ruleForm.sources)"><Plus :size="14" />添加来源</el-button>
        </div>
      </el-form-item>
      <el-form-item label="备注">
        <el-input v-model="ruleForm.note" maxlength="300" show-word-limit />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="ruleDialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="saveRule">
        <CheckCircle2 :size="15" />保存
      </el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="systemDialogOpen" title="系统访问保护" width="620px">
    <el-form label-position="top">
      <el-form-item label="保护状态">
        <el-switch v-model="systemForm.enabled" active-text="启用" inactive-text="停用" />
      </el-form-item>
      <el-form-item label="Panel 控制来源">
        <div class="list-editor">
          <div
            v-for="(_source, index) in systemForm.controlSources"
            :key="index"
            class="source-row"
          >
            <el-input
              v-model="systemForm.controlSources[index]"
              placeholder="198.51.100.10 或 198.51.100.0/24"
            />
            <el-tooltip content="移除">
              <button
                class="icon-control"
                type="button"
                aria-label="移除控制来源"
                :disabled="systemForm.controlSources.length === 1"
                @click="removeSource(systemForm.controlSources, index)"
              ><X :size="15" /></button>
            </el-tooltip>
          </div>
          <el-button plain @click="addSource(systemForm.controlSources)">
            <Plus :size="14" />添加控制来源
          </el-button>
        </div>
      </el-form-item>
      <el-form-item label="临时授权 TTL">
        <el-input-number
          v-model="systemForm.grantTTLSeconds"
          :min="60"
          :max="3600"
          :step="60"
          controls-position="right"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="systemDialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="saveSystem">
        <AlertTriangle :size="15" />应用
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.firewall-toolbar {
  align-items: center;
}

.firewall-heading {
  display: flex;
  align-items: center;
  gap: 10px;
}

.firewall-heading h2,
.firewall-heading p {
  margin: 0;
}

.firewall-heading p {
  margin-top: 3px;
  color: #778179;
  font-size: 11px;
}

.firewall-node-select {
  width: min(260px, 38vw);
}

.node-option {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.node-option small {
  color: #879087;
}

.firewall-content {
  min-height: 180px;
  min-width: 0;
}

.firewall-mobile-rules {
  display: none;
}

.firewall-summary strong {
  min-width: 0;
}

.section-count {
  color: #758078;
  font-size: 11px;
}

.tag-list,
.row-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.row-actions {
  justify-content: flex-end;
}

.danger-icon {
  color: #a64d45;
}

.list-editor {
  display: grid;
  width: 100%;
  gap: 8px;
}

.port-row {
  display: grid;
  grid-template-columns: minmax(130px, 1fr) auto minmax(130px, 1fr) 34px;
  align-items: center;
  gap: 8px;
}

.port-input {
  display: grid;
  min-width: 0;
  gap: 5px;
}

.port-input > span {
  display: none;
  color: #758078;
  font-size: 10px;
  font-weight: 600;
}

.source-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 34px;
  align-items: center;
  gap: 8px;
}

.port-row .el-input-number {
  width: 100%;
}

@media (max-width: 680px) {
  .firewall-toolbar { align-items: stretch; }

  .firewall-toolbar .toolbar-actions {
    display: grid;
    width: 100%;
    grid-template-columns: 36px minmax(0, 1fr);
    gap: 8px;
  }

  .firewall-node-select {
    grid-column: 1 / -1;
    width: 100%;
  }

  .firewall-toolbar .toolbar-actions .el-button {
    width: 100%;
    margin-left: 0;
  }

  .firewall-rule-table { display: none; }
  .firewall-mobile-rules { display: grid; gap: 9px; padding: 10px; }
  .firewall-rule-card { overflow: hidden; border: 1px solid var(--app-border); border-radius: 6px; background: var(--app-surface); }
  .firewall-rule-card header { display: flex; min-height: 48px; align-items: center; justify-content: space-between; gap: 8px; padding: 7px 8px 7px 10px; border-bottom: 1px solid var(--app-border-soft); }
  .firewall-rule-card header > div { display: flex; min-width: 0; align-items: center; gap: 8px; }
  .firewall-rule-card header strong { overflow: hidden; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
  .firewall-rule-card dl { display: grid; grid-template-columns: 52px minmax(0, 1fr); gap: 10px 8px; margin: 0; padding: 11px; font-size: 11px; }
  .firewall-rule-card dt { color: var(--app-text-muted); }
  .firewall-rule-card dd { min-width: 0; margin: 0; overflow-wrap: anywhere; }
  .firewall-rule-card .tag-list { align-items: flex-start; }
  .firewall-rule-card .el-tag { max-width: 100%; }
  .list-editor > .el-button { width: 100%; margin-left: 0; }

  .port-row {
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: stretch;
  }

  .port-input > span { display: block; }
  .port-separator { display: none; }

  .port-row > :nth-child(3) {
    grid-column: 1;
  }

  .port-row > :last-child {
    grid-column: 2;
    grid-row: 1 / 3;
    align-self: center;
  }

  .source-row { align-items: stretch; }
  .source-row .icon-control { align-self: center; }
}
</style>
