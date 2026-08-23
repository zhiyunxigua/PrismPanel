<script setup>
// 迷你趋势图（sparkline）：接收某台服务器最近若干采样点（含 { value } 或裸数字），
// 渲染为无坐标轴的极简折线 + 浅色面积，用于总览对比表格。
import { computed } from "vue";

const props = defineProps({
  points: { type: Array, default: () => [] },
  color: { type: String, default: "#397eaf" },
  width: { type: Number, default: 132 },
  height: { type: Number, default: 26 },
  maxPoints: { type: Number, default: 24 },
});

const values = computed(() => props.points
  .map((point) => (typeof point === "number" ? point : point?.value))
  .filter((value) => Number.isFinite(Number(value)))
  .map((value) => Number(value))
  .slice(-props.maxPoints));

const linePath = computed(() => {
  const list = values.value;
  if (list.length < 2) return "";
  const min = Math.min(...list);
  const max = Math.max(...list);
  const span = max - min || 1;
  const step = props.width / (list.length - 1);
  const usable = Math.max(2, props.height - 4);
  return list.map((value, index) => {
    const x = (index * step).toFixed(1);
    const y = (2 + usable - ((value - min) / span) * usable).toFixed(1);
    return `${x},${y}`;
  }).join(" ");
});

const areaPoints = computed(() => {
  if (!linePath.value) return "";
  return `${linePath.value} ${props.width},${props.height} 0,${props.height}`;
});

const tooltip = computed(() => {
  const last = values.value[values.value.length - 1];
  return last === undefined ? "暂无趋势数据" : `最近在线人数 ${last}`;
});
</script>

<template>
  <svg
    class="mc-sparkline"
    :width="width"
    :height="height"
    :viewBox="`0 0 ${width} ${height}`"
    :title="tooltip"
    role="img"
    aria-label="迷你趋势图"
  >
    <polygon v-if="areaPoints" :points="areaPoints" :fill="color" fill-opacity="0.12" />
    <polyline
      v-if="linePath"
      :points="linePath"
      fill="none"
      :stroke="color"
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
    <circle v-if="values.length === 1" :cx="width / 2" :cy="height / 2" r="2" :fill="color" />
  </svg>
</template>

<style scoped>
.mc-sparkline {
  display: block;
  overflow: visible;
}
</style>
