<script setup>
import { computed, reactive, ref } from "vue";
import { ElMessage } from "element-plus";
import { Mail, Plus, Send, Trash2 } from "lucide-vue-next";
import { request } from "../api";
import { sessionState } from "../session";

const submitting = ref(false);
const formRef = ref();
const form = reactive({
  type: "system",
  broadcast: true,
  senderUUID: "",
  recipients: "",
  subject: "",
  body: "",
  attachments: [],
});
const enabled = computed(() => Boolean(sessionState.features?.mail));
const rules = {
  subject: [{ required: true, message: "请输入邮件标题", trigger: "blur" }],
  body: [{ required: true, message: "请输入邮件正文", trigger: "blur" }],
  senderUUID: [{ required: true, message: "管理员邮件需要发送者 UUID", trigger: "blur" }],
};

function addAttachment() {
  form.attachments.push({ itemKey: "", amount: 1 });
}

function removeAttachment(index) {
  form.attachments.splice(index, 1);
}

async function submit() {
  if (!enabled.value || submitting.value) return;
  const valid = await formRef.value?.validate().catch(() => false);
  if (!valid) return;
  const recipients = form.recipients.split(/\r?\n|,/).map((value) => value.trim()).filter(Boolean);
  if (!form.broadcast && !recipients.length) {
    ElMessage.error("请填写至少一个收件人 UUID");
    return;
  }
  if (form.broadcast && recipients.length) {
    ElMessage.error("全体邮件不能填写收件人");
    return;
  }
  submitting.value = true;
  try {
    await request("/api/v1/mail/send", {
      method: "POST",
      body: JSON.stringify({
        type: form.type,
        sender_uuid: form.type === "admin" ? form.senderUUID.trim() : undefined,
        recipients: form.broadcast ? undefined : recipients,
        broadcast: form.broadcast,
        subject: form.subject.trim(),
        body: form.body,
        attachments: form.attachments.map((item) => ({ item_key: item.itemKey.trim(), amount: Number(item.amount) })),
      }),
    });
    ElMessage.success("邮件已发送");
    Object.assign(form, { type: "system", broadcast: true, senderUUID: "", recipients: "", subject: "", body: "", attachments: [] });
  } catch (error) {
    ElMessage.error(error.message || "邮件发送失败");
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <div class="content-stack">
    <div class="page-toolbar">
      <div><h2>发送邮件</h2><p>通过 PlayerData 向玩家投递系统或管理员邮件</p></div>
      <el-tag v-if="enabled" type="success" effect="plain"><Mail :size="13" /> 已启用</el-tag>
      <el-tag v-else type="info" effect="plain">功能未启用</el-tag>
    </div>

    <section v-if="enabled" class="mail-compose">
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent="submit">
        <div class="mail-grid">
          <el-form-item label="邮件类型">
            <el-radio-group v-model="form.type">
              <el-radio-button label="system">系统邮件</el-radio-button>
              <el-radio-button label="admin">管理员邮件</el-radio-button>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="发送范围">
            <el-radio-group v-model="form.broadcast">
              <el-radio-button :label="true">全体玩家</el-radio-button>
              <el-radio-button :label="false">指定玩家</el-radio-button>
            </el-radio-group>
          </el-form-item>
        </div>
        <el-form-item v-if="form.type === 'admin'" label="发送者 UUID" prop="senderUUID">
          <el-input v-model="form.senderUUID" placeholder="PlayerData 中的玩家 UUID" />
        </el-form-item>
        <el-form-item v-if="!form.broadcast" label="收件人 UUID" required>
          <el-input v-model="form.recipients" type="textarea" :rows="3" placeholder="每行一个 UUID，也可用逗号分隔" />
        </el-form-item>
        <el-form-item label="标题" prop="subject"><el-input v-model="form.subject" maxlength="128" show-word-limit /></el-form-item>
        <el-form-item label="正文" prop="body"><el-input v-model="form.body" type="textarea" :rows="8" maxlength="8192" show-word-limit /></el-form-item>
        <div class="mail-attachments">
          <div class="mail-section-heading"><strong>附件</strong><el-button text type="primary" @click="addAttachment"><Plus :size="15" /> 添加附件</el-button></div>
          <div v-for="(attachment, index) in form.attachments" :key="index" class="mail-attachment-row">
            <el-input v-model="attachment.itemKey" placeholder="item_key" />
            <el-input-number v-model="attachment.amount" :min="1" :max="2147483647" />
            <el-button text type="danger" aria-label="删除附件" @click="removeAttachment(index)"><Trash2 :size="16" /></el-button>
          </div>
          <span v-if="!form.attachments.length" class="mail-muted">无附件</span>
        </div>
        <div class="mail-actions"><el-button type="primary" :loading="submitting" @click="submit"><Send :size="15" />发送邮件</el-button></div>
      </el-form>
    </section>
    <section v-else class="empty-resource"><Mail :size="26" /><strong>邮件功能未启用</strong><span>请在面板配置中启用 features.mail 并配置 PlayerData。</span></section>
  </div>
</template>

<style scoped>
.mail-compose { max-width: 860px; border: 1px solid var(--app-border); padding: 22px; background: var(--app-surface); }
.mail-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 18px; }
.mail-section-heading, .mail-actions { display: flex; align-items: center; justify-content: space-between; }
.mail-attachments { margin-top: 8px; border-top: 1px solid var(--app-border); padding-top: 14px; }
.mail-attachment-row { display: grid; grid-template-columns: 1fr 180px 42px; gap: 8px; margin-top: 8px; }
.mail-muted { display: block; margin-top: 8px; color: var(--app-text-muted); font-size: 13px; }
.mail-actions { justify-content: flex-end; margin-top: 22px; }
@media (max-width: 700px) { .mail-compose { padding: 16px; } .mail-grid { grid-template-columns: 1fr; gap: 0; } .mail-attachment-row { grid-template-columns: 1fr 130px 36px; } }
</style>
