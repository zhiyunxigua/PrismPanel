<script setup>
import { reactive, ref } from "vue";
import { useRouter } from "vue-router";
import { Boxes, Cable } from "lucide-vue-next";
import { resetSession } from "../session";
import { configurePanelURL, runtimeConfig } from "../runtime";

const router = useRouter();
const formRef = ref();
const submitting = ref(false);
const errorMessage = ref(runtimeConfig.connectionError || "");
const form = reactive({ panelURL: runtimeConfig.panelUrl || "" });
const rules = {
  panelURL: [
    { required: true, message: "请输入 Panel 地址", trigger: "blur" },
    {
      validator: (_rule, value, callback) => {
        try {
          const parsed = new URL(String(value || "").trim());
          if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
              (parsed.pathname !== "/" && parsed.pathname !== "")) {
            callback(new Error("请输入 http 或 https 的 Panel 根地址"));
            return;
          }
          callback();
        } catch {
          callback(new Error("请输入完整的 Panel 地址"));
        }
      },
      trigger: "blur",
    },
  ],
};

async function submit() {
  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) return;
  submitting.value = true;
  errorMessage.value = "";
  try {
    await configurePanelURL(form.panelURL.trim());
    resetSession();
    await router.replace({ name: "login" });
  } catch (error) {
    errorMessage.value = error.message || "无法连接远程 Panel";
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <main class="login-screen">
    <section class="login-form-wrap" aria-labelledby="panel-setup-title">
      <div class="login-product">
        <span class="login-brand-mark"><Boxes :size="21" /></span>
        <div><strong>PrismPanel</strong><span>Windows 客户端</span></div>
      </div>
      <h1 id="panel-setup-title">连接面板</h1>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent="submit">
        <el-form-item label="Panel 地址" prop="panelURL">
          <el-input v-model="form.panelURL" placeholder="https://panel.example.com" autofocus />
        </el-form-item>
        <div v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</div>
        <el-button class="login-submit" type="primary" :loading="submitting" native-type="submit">
          <Cable v-if="!submitting" :size="16" />
          连接
        </el-button>
        <el-button v-if="runtimeConfig.configured" class="login-panel-settings" text @click="router.back()">
          取消
        </el-button>
      </el-form>
    </section>
  </main>
</template>
