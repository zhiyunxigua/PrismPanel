<script setup>
import { computed } from "vue";
import { useRoute } from "vue-router";
import { FolderOpen } from "lucide-vue-next";

const route = useRoute();
const resource = computed(() => route.meta.resource || route.meta.title);
</script>

<template>
  <div class="content-stack">
    <div class="page-toolbar">
      <div>
        <h2>{{ route.meta.title }}</h2>
        <p>0 条记录</p>
      </div>
    </div>
    <section class="empty-resource">
      <FolderOpen :size="26" />
      <strong>暂无{{ resource }}</strong>
    </section>
  </div>
</template>
