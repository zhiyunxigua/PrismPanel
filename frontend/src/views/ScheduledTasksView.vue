<script setup>
import { computed, onMounted, ref } from "vue";
import {
  CalendarClock, Edit3, Play, Plus, RefreshCw, Search, Trash2,
} from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { request } from "../api";
import InstanceSelectionTree from "../components/InstanceSelectionTree.vue";
import { hasPermission } from "../session";

const loading = ref(false);
const submitting = ref(false);
const dialogOpen = ref(false);
const editingID = ref("");
const tasks = ref([]);
const nodeContents = ref([]);
const search = ref("");
const actionFilter = ref("");
const enabledFilter = ref("");
const form = ref(defaultForm());

const canManage = computed(() => hasPermission("schedule.manage"));
const filteredTasks = computed(() => {
  const keyword = search.value.trim().toLowerCase();
  return tasks.value.filter((task) => (
    (!keyword || [task.name, task.action_payload, task.created_by_display_name]
      .join(" ").toLowerCase().includes(keyword))
    && (!actionFilter.value || task.action_type === actionFilter.value)
    && (enabledFilter.value === "" || task.enabled === enabledFilter.value)
  ));
});

const actionLabels = {
  start: "启动实例",
  stop: "停止实例",
  restart: "重启实例",
  kill: "强制停止",
  command: "发送控制台命令",
};

const weekdayLabels = ["一", "二", "三", "四", "五", "六", "日"];

function defaultForm() {
  return {
    name: "",
    enabled: true,
    scheduleType: "daily",
    timezone: "Asia/Shanghai",
    onceRunAt: shanghaiDateInput(new Date(Date.now() + 60 * 60 * 1000)),
    clockTime: "04:00:00",
    weekdays: [1],
    intervalValue: 1,
    intervalUnit: "hours",
    actionType: "restart",
    actionPayload: "",
    targets: [],
  };
}

async function load(silent = false) {
  if (!silent) loading.value = true;
  try {
    const responses = await Promise.all([
      request("/api/v1/scheduled-tasks"),
      canManage.value ? request("/api/v1/servers") : Promise.resolve({ nodes: [] }),
    ]);
    tasks.value = responses[0].items || [];
    nodeContents.value = responses[1].nodes || [];
  } catch (error) {
    if (!silent) ElMessage.error(error.message);
  } finally {
    if (!silent) loading.value = false;
  }
}

function openCreate() {
  editingID.value = "";
  form.value = defaultForm();
  dialogOpen.value = true;
}

function openEdit(task) {
  const schedule = task.schedule || {};
  const interval = intervalForm(schedule.interval_seconds || 3600);
  editingID.value = task.id;
  form.value = {
    name: task.name,
    enabled: task.enabled,
    scheduleType: task.schedule_type,
    timezone: task.timezone || "Asia/Shanghai",
    onceRunAt: schedule.run_at ? shanghaiDateInput(new Date(schedule.run_at)) : "",
    clockTime: schedule.time || "04:00:00",
    weekdays: [...(schedule.weekdays || [1])],
    intervalValue: interval.value,
    intervalUnit: interval.unit,
    actionType: task.action_type,
    actionPayload: task.action_payload || "",
    targets: (task.targets || []).map((target) => ({ ...target })),
  };
  dialogOpen.value = true;
}

function intervalForm(seconds) {
  if (seconds % 86400 === 0) return { value: seconds / 86400, unit: "days" };
  if (seconds % 3600 === 0) return { value: seconds / 3600, unit: "hours" };
  return { value: Math.max(1, Math.round(seconds / 60)), unit: "minutes" };
}

function buildPayload(source = form.value) {
  const schedule = {};
  if (source.scheduleType === "once") {
    schedule.run_at = source.onceRunAt.replace(" ", "T") + "+08:00";
  }
  if (source.scheduleType === "daily") schedule.time = source.clockTime;
  if (source.scheduleType === "weekly") {
    schedule.time = source.clockTime;
    schedule.weekdays = source.weekdays;
  }
  if (source.scheduleType === "interval") {
    const multipliers = { minutes: 60, hours: 3600, days: 86400 };
    schedule.interval_seconds = source.intervalValue * multipliers[source.intervalUnit];
  }
  return {
    name: source.name.trim(),
    enabled: source.enabled,
    schedule_type: source.scheduleType,
    schedule,
    timezone: source.timezone,
    action_type: source.actionType,
    action_payload: source.actionType === "command" ? source.actionPayload.trim() : "",
    targets: source.targets,
  };
}

function taskPayload(task, changes = {}) {
  return {
    name: task.name,
    enabled: task.enabled,
    schedule_type: task.schedule_type,
    schedule: task.schedule,
    timezone: task.timezone,
    action_type: task.action_type,
    action_payload: task.action_payload || "",
    targets: task.targets || [],
    ...changes,
  };
}

function validateForm() {
  if (!form.value.name.trim()) return "请输入任务名称";
  if (!form.value.targets.length) return "请至少选择一个实例";
  if (form.value.scheduleType === "once" && !form.value.onceRunAt) return "请选择执行时间";
  if (form.value.scheduleType === "weekly" && !form.value.weekdays.length) return "请至少选择一天";
  if (form.value.actionType === "command" && !form.value.actionPayload.trim()) return "请输入控制台命令";
  return "";
}

async function submit() {
  const message = validateForm();
  if (message) {
    ElMessage.warning(message);
    return;
  }
  submitting.value = true;
  try {
    const path = editingID.value
      ? "/api/v1/scheduled-tasks/" + encodeURIComponent(editingID.value)
      : "/api/v1/scheduled-tasks";
    await request(path, {
      method: editingID.value ? "PUT" : "POST",
      body: JSON.stringify(buildPayload()),
    });
    dialogOpen.value = false;
    ElMessage.success(editingID.value ? "定时任务已更新" : "定时任务已创建");
    await load(true);
  } catch (error) {
    ElMessage.error(error.message);
  } finally {
    submitting.value = false;
  }
}

async function toggleTask(task, enabled) {
  const original = task.enabled;
  task.enabled = enabled;
  try {
    const updated = await request("/api/v1/scheduled-tasks/" + encodeURIComponent(task.id), {
      method: "PUT",
      body: JSON.stringify(taskPayload(task, { enabled })),
    });
    Object.assign(task, updated);
    ElMessage.success(enabled ? "任务已启用" : "任务已停用");
  } catch (error) {
    task.enabled = original;
    ElMessage.error(error.message);
  }
}

async function runNow(task) {
  try {
    await request("/api/v1/scheduled-tasks/" + encodeURIComponent(task.id) + "/run", {
      method: "POST", body: "{}",
    });
    ElMessage.success("任务已开始执行");
    await load(true);
  } catch (error) {
    ElMessage.error(error.message);
  }
}

async function removeTask(task) {
  try {
    await ElMessageBox.confirm(
      "删除后不会影响已有执行日志。",
      "删除定时任务",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" },
    );
    await request("/api/v1/scheduled-tasks/" + encodeURIComponent(task.id), {
      method: "DELETE",
    });
    ElMessage.success("定时任务已删除");
    await load(true);
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(error.message);
  }
}

function scheduleLabel(task) {
  const value = task.schedule || {};
  if (task.schedule_type === "once") return "单次 · " + formatDate(value.run_at);
  if (task.schedule_type === "daily") return "每天 " + (value.time || "");
  if (task.schedule_type === "weekly") {
    const days = (value.weekdays || []).map((day) => "周" + weekdayLabels[day - 1]).join("、");
    return days + " " + (value.time || "");
  }
  if (task.schedule_type === "interval") return "每 " + durationLabel(value.interval_seconds);
  return task.schedule_type;
}

function durationLabel(seconds) {
  if (seconds % 86400 === 0) return seconds / 86400 + " 天";
  if (seconds % 3600 === 0) return seconds / 3600 + " 小时";
  return seconds / 60 + " 分钟";
}

function formatDate(value) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  }).format(new Date(value));
}

function shanghaiDateInput(date) {
  const parts = new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
  }).formatToParts(date).reduce((result, item) => {
    result[item.type] = item.value;
    return result;
  }, {});
  return parts.year + "-" + parts.month + "-" + parts.day
    + " " + parts.hour + ":" + parts.minute + ":" + parts.second;
}

onMounted(load);
</script>

<template>
  <div class="content-stack">
    <div class="page-toolbar">
      <div>
        <h2>定时任务</h2>
        <p>{{ tasks.length }} 条任务 · {{ tasks.filter((task) => task.enabled).length }} 条已启用</p>
      </div>
      <div class="toolbar-actions">
        <el-tooltip content="刷新">
          <el-button class="square-button" :loading="loading" aria-label="刷新" @click="load()">
            <RefreshCw v-if="!loading" :size="16" />
          </el-button>
        </el-tooltip>
        <el-button v-if="canManage" type="primary" @click="openCreate">
          <Plus :size="16" />新建任务
        </el-button>
      </div>
    </div>

    <div class="table-toolbar scheduled-task-filters">
      <el-input v-model="search" class="search-input" clearable placeholder="搜索任务名称或命令">
        <template #prefix><Search :size="15" /></template>
      </el-input>
      <el-select v-model="actionFilter" class="status-filter" clearable placeholder="全部动作">
        <el-option v-for="(label, value) in actionLabels" :key="value" :label="label" :value="value" />
      </el-select>
      <el-select v-model="enabledFilter" class="status-filter" clearable placeholder="全部状态">
        <el-option label="已启用" :value="true" />
        <el-option label="已停用" :value="false" />
      </el-select>
    </div>

    <div v-loading="loading" class="table-frame">
      <el-table :data="filteredTasks" row-key="id">
        <el-table-column label="任务" min-width="210">
          <template #default="{ row }">
            <div class="scheduled-task-name">
              <span><CalendarClock :size="16" /></span>
              <div><strong>{{ row.name }}</strong><small>{{ row.timezone }}</small></div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="动作" min-width="180">
          <template #default="{ row }">
            <strong>{{ actionLabels[row.action_type] || row.action_type }}</strong>
            <small v-if="row.action_payload" class="block muted command-preview">{{ row.action_payload }}</small>
          </template>
        </el-table-column>
        <el-table-column label="规则" min-width="210">
          <template #default="{ row }">{{ scheduleLabel(row) }}</template>
        </el-table-column>
        <el-table-column label="目标" width="78">
          <template #default="{ row }">{{ row.targets?.length || 0 }}</template>
        </el-table-column>
        <el-table-column label="下次执行" min-width="166">
          <template #default="{ row }">{{ row.enabled ? formatDate(row.next_run_at) : "-" }}</template>
        </el-table-column>
        <el-table-column label="上次执行" min-width="166">
          <template #default="{ row }">{{ formatDate(row.last_run_at) }}</template>
        </el-table-column>
        <el-table-column label="启用" width="74">
          <template #default="{ row }">
            <el-switch
              :model-value="row.enabled"
              :disabled="!canManage"
              @change="toggleTask(row, $event)"
            />
          </template>
        </el-table-column>
        <el-table-column v-if="canManage" label="操作" width="132" align="right">
          <template #default="{ row }">
            <div class="scheduled-task-actions">
              <el-tooltip content="立即执行">
                <button class="table-action" type="button" aria-label="立即执行" @click="runNow(row)">
                  <Play :size="15" />
                </button>
              </el-tooltip>
              <el-tooltip content="编辑">
                <button class="table-action" type="button" aria-label="编辑" @click="openEdit(row)">
                  <Edit3 :size="15" />
                </button>
              </el-tooltip>
              <el-tooltip content="删除">
                <button class="table-action danger" type="button" aria-label="删除" @click="removeTask(row)">
                  <Trash2 :size="15" />
                </button>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>
        <template #empty>
          <div class="table-empty"><CalendarClock :size="24" /><span>暂无定时任务</span></div>
        </template>
      </el-table>
    </div>
  </div>

  <el-dialog
    v-model="dialogOpen"
    :title="editingID ? '编辑定时任务' : '新建定时任务'"
    width="780px"
    destroy-on-close
  >
    <el-form :model="form" label-position="top" @submit.prevent="submit">
      <div class="scheduled-form-top">
        <el-form-item label="任务名称">
          <el-input v-model="form.name" maxlength="100" show-word-limit />
        </el-form-item>
        <el-form-item label="状态">
          <el-switch v-model="form.enabled" active-text="启用" inactive-text="停用" />
        </el-form-item>
      </div>

      <el-form-item label="定时规则">
        <el-radio-group v-model="form.scheduleType">
          <el-radio-button value="once">单次</el-radio-button>
          <el-radio-button value="daily">每天</el-radio-button>
          <el-radio-button value="weekly">每周</el-radio-button>
          <el-radio-button value="interval">固定间隔</el-radio-button>
        </el-radio-group>
      </el-form-item>

      <div class="scheduled-rule-row">
        <el-form-item v-if="form.scheduleType === 'once'" label="执行时间">
          <el-date-picker
            v-model="form.onceRunAt"
            type="datetime"
            format="YYYY-MM-DD HH:mm"
            value-format="YYYY-MM-DD HH:mm:ss"
            placeholder="选择执行时间"
          />
        </el-form-item>
        <el-form-item v-if="['daily', 'weekly'].includes(form.scheduleType)" label="执行时间">
          <el-time-picker
            v-model="form.clockTime"
            format="HH:mm:ss"
            value-format="HH:mm:ss"
            placeholder="选择时间"
          />
        </el-form-item>
        <el-form-item v-if="form.scheduleType === 'weekly'" label="星期">
          <el-checkbox-group v-model="form.weekdays" class="weekday-selector">
            <el-checkbox-button v-for="(label, index) in weekdayLabels" :key="label" :value="index + 1">
              {{ label }}
            </el-checkbox-button>
          </el-checkbox-group>
        </el-form-item>
        <el-form-item v-if="form.scheduleType === 'interval'" label="间隔">
          <div class="interval-input">
            <el-input-number v-model="form.intervalValue" :min="1" :max="525600" controls-position="right" />
            <el-select v-model="form.intervalUnit">
              <el-option label="分钟" value="minutes" />
              <el-option label="小时" value="hours" />
              <el-option label="天" value="days" />
            </el-select>
          </div>
        </el-form-item>
        <el-form-item label="时区">
          <el-select v-model="form.timezone">
            <el-option label="Asia/Shanghai (UTC+8)" value="Asia/Shanghai" />
          </el-select>
        </el-form-item>
      </div>

      <div class="scheduled-action-row">
        <el-form-item label="执行动作">
          <el-select v-model="form.actionType">
            <el-option v-for="(label, value) in actionLabels" :key="value" :label="label" :value="value" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="form.actionType === 'command'" label="控制台命令">
          <el-input v-model="form.actionPayload" maxlength="2000" placeholder="例如：say Server maintenance" />
        </el-form-item>
      </div>

      <el-form-item label="目标实例">
        <InstanceSelectionTree v-model="form.targets" :nodes="nodeContents" :disabled="submitting" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="dialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">保存</el-button>
    </template>
  </el-dialog>
</template>
