<script setup>
import { computed, nextTick, onMounted, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { Boxes, LogIn, Moon, Settings, Sun, Trash2 } from "lucide-vue-next";
import { ElMessage, ElMessageBox } from "element-plus";
import { login, loginSavedAccount, sessionState } from "../session";
import {
  deleteSavedAccountWinApp, isWinApp, runtimeConfig, savedAccounts,
} from "../runtime";
import { currentTheme, toggleTheme } from "../theme";

const route = useRoute();
const router = useRouter();
const isDarkTheme = computed(() => currentTheme.value === "dark");
const formRef = ref();
const usernameInput = ref();
const submitting = ref(false);
const loadingAccounts = ref(false);
const errorMessage = ref(runtimeConfig.autoLoginError || runtimeConfig.connectionError || "");
const accounts = ref([]);
const selectedAccountID = ref("");
const manualMode = ref(true);
const form = reactive({ username: "", password: "", remember: false });
const savedMode = computed(() => (
  isWinApp() && sessionState.initialized && accounts.value.length > 0 && !manualMode.value
));
const rules = {
  username: [
    { required: true, message: "请输入用户名", trigger: "blur" },
    {
      validator: (_rule, value, callback) => {
        if (!sessionState.initialized && !/^[a-z0-9][a-z0-9._-]{2,31}$/.test(value)) {
          callback(new Error("使用 3-32 位小写字母、数字、点、下划线或连字符"));
        } else callback();
      },
      trigger: "blur",
    },
  ],
  password: [{
    validator: (_rule, value, callback) => {
      if (!value) callback(new Error("请输入密码"));
      else if (!sessionState.initialized && Array.from(value).length < 6) callback(new Error("密码至少需要 6 个字符"));
      else callback();
    },
    trigger: "blur",
  }],
};

onMounted(loadSavedAccounts);

async function loadSavedAccounts() {
  if (!isWinApp() || !sessionState.initialized) return;
  loadingAccounts.value = true;
  try {
    accounts.value = await savedAccounts();
    if (accounts.value.length) {
      selectedAccountID.value = accounts.value[0].id;
      manualMode.value = false;
    }
  } catch (error) {
    if (!errorMessage.value) errorMessage.value = error.message || "无法读取 Windows 已保存账号";
  } finally {
    loadingAccounts.value = false;
  }
}

async function submitManual() {
  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) return;
  await runLogin(() => login(form.username, form.password, isWinApp() && form.remember));
}

async function submitSaved() {
  if (!selectedAccountID.value) return;
  await runLogin(() => loginSavedAccount(selectedAccountID.value));
}

async function runLogin(operation) {
  submitting.value = true;
  errorMessage.value = "";
  try {
    const result = await operation();
    if (result?.credential_warning) ElMessage.warning(result.credential_warning);
    const redirect = typeof route.query.redirect === "string" ? route.query.redirect : "/";
    await router.replace(redirect);
  } catch (error) {
    errorMessage.value = error.message || "用户名或密码错误";
  } finally {
    submitting.value = false;
  }
}

async function useOtherAccount() {
  manualMode.value = true;
  errorMessage.value = "";
  form.username = "";
  form.password = "";
  form.remember = false;
  await nextTick();
  usernameInput.value?.focus?.();
}

function returnToSavedAccounts() {
  manualMode.value = false;
  errorMessage.value = "";
}

async function removeSavedAccount() {
  const account = accounts.value.find((item) => item.id === selectedAccountID.value);
  if (!account || submitting.value) return;
  try {
    await ElMessageBox.confirm(
      `将从 Windows 中删除已保存账号“${account.username}”。`,
      "删除已保存账号",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" },
    );
    accounts.value = await deleteSavedAccountWinApp(account.id);
    selectedAccountID.value = accounts.value[0]?.id || "";
    if (!accounts.value.length) manualMode.value = true;
  } catch (error) {
    if (error !== "cancel" && error !== "close") {
      errorMessage.value = error.message || "删除已保存账号失败";
    }
  }
}

function formatLoginTime(value) {
  if (!value) return "未知时间";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
  }).format(new Date(value));
}
</script>

<template>
  <main class="login-screen">
    <el-tooltip :content="isDarkTheme ? '切换为亮色主题' : '切换为暗色主题'" placement="left">
      <button
        class="icon-control login-theme-control"
        type="button"
        :aria-label="isDarkTheme ? '切换为亮色主题' : '切换为暗色主题'"
        :aria-pressed="isDarkTheme"
        @click="toggleTheme"
      >
        <Sun v-if="isDarkTheme" :size="18" />
        <Moon v-else :size="18" />
      </button>
    </el-tooltip>
    <section v-loading="loadingAccounts" class="login-form-wrap" aria-labelledby="login-title">
      <div class="login-product">
        <span class="login-brand-mark"><Boxes :size="21" /></span>
        <div><strong>PrismPanel</strong><span>{{ isWinApp() ? "Windows 客户端" : "控制面板" }}</span></div>
      </div>
      <h1 id="login-title">{{ sessionState.initialized ? "登录" : "创建超级管理员" }}</h1>

      <form v-if="savedMode" class="saved-login" @submit.prevent="submitSaved">
        <label class="saved-login-label" for="saved-account">账号</label>
        <div class="saved-account-row">
          <el-select id="saved-account" v-model="selectedAccountID" :disabled="submitting" size="large">
            <el-option v-for="account in accounts" :key="account.id" :value="account.id" :label="account.username">
              <div class="saved-account-option">
                <strong>{{ account.username }}</strong>
                <small>{{ formatLoginTime(account.last_login_at) }}</small>
              </div>
            </el-option>
          </el-select>
          <el-tooltip content="删除已保存账号">
            <button class="saved-account-delete" type="button" :disabled="submitting" @click="removeSavedAccount">
              <Trash2 :size="16" />
            </button>
          </el-tooltip>
        </div>
        <div v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</div>
        <div class="saved-login-actions">
          <el-button type="primary" :loading="submitting" native-type="submit">
            <LogIn v-if="!submitting" :size="16" />登录
          </el-button>
          <el-button :disabled="submitting" @click="useOtherAccount">使用其他账号登录</el-button>
        </div>
      </form>

      <el-form
        v-else
        ref="formRef"
        :model="form"
        :rules="rules"
        label-position="top"
        @submit.prevent="submitManual"
      >
        <el-form-item label="用户名" prop="username">
          <el-input ref="usernameInput" v-model="form.username" autocomplete="username" autofocus />
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input
            v-model="form.password"
            type="password"
            show-password
            :autocomplete="sessionState.initialized ? 'current-password' : 'new-password'"
          />
        </el-form-item>
        <el-checkbox v-if="isWinApp()" v-model="form.remember" class="remember-account">
          保存账号和密码
        </el-checkbox>
        <div v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</div>
        <el-button class="login-submit" type="primary" :loading="submitting" native-type="submit">
          <LogIn v-if="!submitting" :size="16" />
          {{ sessionState.initialized ? "登录" : "创建并登录" }}
        </el-button>
        <el-button
          v-if="accounts.length"
          class="login-panel-settings"
          text
          :disabled="submitting"
          @click="returnToSavedAccounts"
        >
          返回已保存账号
        </el-button>
      </el-form>
      <el-button v-if="isWinApp()" class="login-panel-settings" text @click="router.push({ name: 'panel-setup' })">
        <Settings :size="15" />面板地址
      </el-button>
    </section>
  </main>
</template>
