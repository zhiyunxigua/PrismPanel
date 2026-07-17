<script setup>
import { reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { Boxes, LogIn, Settings } from "lucide-vue-next";
import { login, sessionState } from "../session";
import { isWinApp } from "../runtime";

const route = useRoute();
const router = useRouter();
const formRef = ref();
const submitting = ref(false);
const errorMessage = ref("");
const form = reactive({ username: "", password: "" });
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

async function submit() {
  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) return;
  submitting.value = true;
  errorMessage.value = "";
  try {
    await login(form.username, form.password);
    const redirect = typeof route.query.redirect === "string" ? route.query.redirect : "/";
    await router.replace(redirect);
  } catch (error) {
    errorMessage.value = error.message || "用户名或密码错误";
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <main class="login-screen">
    <section class="login-form-wrap" aria-labelledby="login-title">
      <div class="login-product">
        <span class="login-brand-mark"><Boxes :size="21" /></span>
        <div><strong>PrismPanel</strong><span>{{ isWinApp() ? "Windows 客户端" : "控制面板" }}</span></div>
      </div>
      <h1 id="login-title">{{ sessionState.initialized ? "登录" : "创建超级管理员" }}</h1>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent="submit">
        <el-form-item label="用户名" prop="username">
          <el-input v-model="form.username" autocomplete="username" autofocus />
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input
            v-model="form.password"
            type="password"
            show-password
            :autocomplete="sessionState.initialized ? 'current-password' : 'new-password'"
          />
        </el-form-item>
        <div v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</div>
        <el-button class="login-submit" type="primary" :loading="submitting" native-type="submit">
          <LogIn v-if="!submitting" :size="16" />
          {{ sessionState.initialized ? "登录" : "创建并登录" }}
        </el-button>
      </el-form>
      <el-button v-if="isWinApp()" class="login-panel-settings" text @click="router.push({ name: 'panel-setup' })">
        <Settings :size="15" />
        面板地址
      </el-button>
    </section>
  </main>
</template>
