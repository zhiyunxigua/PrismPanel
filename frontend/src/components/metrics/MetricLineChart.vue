<script setup>
import { computed } from "vue";

const props = defineProps({
  title: { type: String, required: true },
  points: { type: Array, default: () => [] },
  color: { type: String, default: "#397eaf" },
  unit: { type: String, default: "" },
  decimals: { type: Number, default: 1 },
  maximum: { type: Number, default: null },
});

const width = 600;
const height = 168;
const top = 12;
const bottom = 24;
const chartHeight = height - top - bottom;

const normalized = computed(() => props.points.map((point) => {
  const raw = point?.value;
  const value = raw === null || raw === undefined ? Number.NaN : Number(raw);
  return {
    sampledAt: point?.sampled_at,
    value: Number.isFinite(value) ? value : null,
  };
}));
const values = computed(() => normalized.value.flatMap((point) => point.value === null ? [] : [point.value]));
const latest = computed(() => {
  for (let index = normalized.value.length - 1; index >= 0; index--) {
    if (normalized.value[index].value !== null) return normalized.value[index].value;
  }
  return null;
});
const chartMaximum = computed(() => {
  if (Number.isFinite(props.maximum) && props.maximum > 0) return props.maximum;
  const highest = Math.max(0, ...values.value);
  if (highest <= 0) return 1;
  return Math.max(1, Math.ceil(highest * 1.12));
});
const paths = computed(() => {
  const result = [];
  let current = [];
  const count = normalized.value.length;
  normalized.value.forEach((point, index) => {
    if (point.value === null) {
      if (current.length > 1) result.push(current.join(" "));
      current = [];
      return;
    }
    const x = count <= 1 ? width / 2 : (index / (count - 1)) * width;
    const ratio = Math.min(1, Math.max(0, point.value / chartMaximum.value));
    const y = top + chartHeight * (1 - ratio);
    current.push(x.toFixed(2) + "," + y.toFixed(2));
  });
  if (current.length > 1) result.push(current.join(" "));
  return result;
});
const startTime = computed(() => formatTime(normalized.value[0]?.sampledAt));
const endTime = computed(() => formatTime(normalized.value.at(-1)?.sampledAt));

function formatValue(value) {
  return Number.isFinite(value) ? value.toFixed(props.decimals) + props.unit : "--";
}

function formatTime(value) {
  if (!value) return "--:--";
  return new Intl.DateTimeFormat("zh-CN", {
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  }).format(new Date(value));
}
</script>

<template>
  <section class="metric-chart">
    <header>
      <span>{{ title }}</span>
      <strong :style="{ color }">{{ formatValue(latest) }}</strong>
    </header>
    <div class="metric-chart-canvas">
      <svg :viewBox="'0 0 ' + width + ' ' + height" role="img" :aria-label="title + '最近十分钟趋势'">
        <line v-for="row in 4" :key="row" x1="0" :y1="top + chartHeight * row / 4" :x2="width" :y2="top + chartHeight * row / 4" class="metric-grid-line" />
        <polyline
          v-for="(path, index) in paths"
          :key="index"
          :points="path"
          :stroke="color"
          class="metric-series-line"
        />
      </svg>
      <div v-if="values.length < 2" class="metric-chart-empty">正在积累性能数据</div>
    </div>
    <footer><span>{{ startTime }}</span><span>最近 10 分钟</span><span>{{ endTime }}</span></footer>
  </section>
</template>

<style scoped>
.metric-chart {
  min-width: 0;
  padding: 13px;
  background: #fff;
  border: 1px solid #dce2dd;
  border-radius: 6px;
}
.metric-chart header, .metric-chart footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.metric-chart header span {
  color: #59665e;
  font-size: 11px;
  font-weight: 700;
}
.metric-chart header strong { font-size: 16px; }
.metric-chart-canvas {
  position: relative;
  width: 100%;
  aspect-ratio: 600 / 168;
  min-height: 120px;
  margin-top: 9px;
  overflow: hidden;
  background: #f8faf9;
}
.metric-chart svg { display: block; width: 100%; height: 100%; }
.metric-grid-line { stroke: #e1e7e3; stroke-width: 1; vector-effect: non-scaling-stroke; }
.metric-series-line {
  fill: none;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;
}
.metric-chart-empty {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: #879188;
  font-size: 11px;
}
.metric-chart footer {
  margin-top: 6px;
  color: #89928c;
  font-size: 9px;
}
</style>
