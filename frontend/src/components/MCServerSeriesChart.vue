<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { LocateFixed } from "lucide-vue-next";

const props = defineProps({
  games: { type: Array, default: () => [] },
  windowStart: { type: [String, Date], default: null },
  windowEnd: { type: [String, Date], default: null },
  loading: { type: Boolean, default: false },
  title: { type: String, default: "在线人数趋势" },
});

const emit = defineEmits(["open-game"]);

const chartCanvas = ref(null);
const chartWidth = ref(1200);
const chartHeight = ref(440);
const plot = { left: 70, right: 24, top: 20, bottom: 58 };
const plotWidth = computed(() => Math.max(1, chartWidth.value - plot.left - plot.right));
const plotHeight = computed(() => Math.max(1, chartHeight.value - plot.top - plot.bottom));
const zoomRange = ref([0, 100]);
const jumpAt = ref("");
const hover = ref(null);

const palette = [
  "#c64c4c", "#397eaf", "#3f8f64", "#d88a2d", "#7a5bb5",
  "#b94d88", "#278b8b", "#8b6537", "#5875c5", "#6f8f3c",
  "#d0603d", "#4f6f62", "#9b4f4f", "#477fa2", "#a06e24",
  "#785f93", "#2f8a69", "#b05b72", "#526f9e", "#708347",
];

const normalizedGames = computed(() => props.games.map((game, index) => ({
  ...game,
  color: palette[index % palette.length],
  points: (game.points || [])
    .map((point) => {
      const time = new Date(point.sampled_at).getTime();
      const value = point.value === null || point.value === undefined ? null : Number(point.value);
      return {
        time,
        sampledAt: point.sampled_at,
        value: Number.isFinite(value) ? value : null,
      };
    })
    .filter((point) => Number.isFinite(point.time))
    .sort((left, right) => left.time - right.time),
})));

const bounds = computed(() => {
  const pointTimes = normalizedGames.value.flatMap((game) => game.points.map((point) => point.time));
  const configuredStart = new Date(props.windowStart || "").getTime();
  const configuredEnd = new Date(props.windowEnd || "").getTime();
  let start = Number.isFinite(configuredStart) ? configuredStart : Math.min(...pointTimes);
  let end = Number.isFinite(configuredEnd) ? configuredEnd : Math.max(...pointTimes);
  if (!Number.isFinite(start) || !Number.isFinite(end)) {
    end = Date.now();
    start = end - 24 * 60 * 60 * 1000;
  }
  if (end <= start) end = start + 60 * 1000;
  return { start, end };
});

const visibleBounds = computed(() => {
  const duration = bounds.value.end - bounds.value.start;
  return {
    start: bounds.value.start + duration * zoomRange.value[0] / 100,
    end: bounds.value.start + duration * zoomRange.value[1] / 100,
  };
});

const visibleValues = computed(() => normalizedGames.value.flatMap((game) => game.points
  .filter((point) => point.time >= visibleBounds.value.start && point.time <= visibleBounds.value.end && point.value !== null)
  .map((point) => point.value)));

const yMaximum = computed(() => {
  const maximum = Math.max(0, ...visibleValues.value);
  if (maximum <= 0) return 1;
  const magnitude = 10 ** Math.floor(Math.log10(maximum));
  const normalized = maximum / magnitude;
  const nice = normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10;
  return nice * magnitude;
});

const yTicks = computed(() => Array.from({ length: 6 }, (_, index) => {
  const ratio = index / 5;
  const value = yMaximum.value * (1 - ratio);
  return { y: plot.top + plotHeight.value * ratio, value };
}));

const xTicks = computed(() => Array.from({ length: 6 }, (_, index) => {
  const ratio = index / 5;
  const time = visibleBounds.value.start + (visibleBounds.value.end - visibleBounds.value.start) * ratio;
  return { x: plot.left + plotWidth.value * ratio, time };
}));

function xForTime(time) {
  const duration = visibleBounds.value.end - visibleBounds.value.start;
  return plot.left + (time - visibleBounds.value.start) / duration * plotWidth.value;
}

function yForValue(value) {
  return plot.top + plotHeight.value * (1 - Math.max(0, value) / yMaximum.value);
}

function pointsInView(points) {
  if (!points.length) return [];
  const result = points.filter((point) => point.time >= visibleBounds.value.start && point.time <= visibleBounds.value.end);
  const before = [...points].reverse().find((point) => point.time < visibleBounds.value.start);
  const after = points.find((point) => point.time > visibleBounds.value.end);
  if (before) result.unshift(before);
  if (after) result.push(after);
  return result;
}

function gamePaths(game) {
  const paths = [];
  let segment = [];
  for (const point of pointsInView(game.points)) {
    if (point.value === null) {
      if (segment.length > 1) paths.push(segment.join(" "));
      segment = [];
      continue;
    }
    segment.push(xForTime(point.time).toFixed(2) + "," + yForValue(point.value).toFixed(2));
  }
  if (segment.length > 1) paths.push(segment.join(" "));
  return paths;
}

const renderedGames = computed(() => normalizedGames.value.map((game) => ({
  ...game,
  paths: gamePaths(game),
})));

const hoverMarkers = computed(() => {
  if (!hover.value) return [];
  return normalizedGames.value.flatMap((game) => {
    const point = game.points.find((item) => item.time === hover.value.time && item.value !== null);
    if (!point) return [];
    return [{
      gameID: game.game_id,
      name: game.name || game.game_id,
      color: game.color,
      value: point.value,
      x: xForTime(point.time),
      y: yForValue(point.value),
      active: game.game_id === hover.value.gameID,
    }];
  }).sort((left, right) => right.value - left.value || left.name.localeCompare(right.name, "zh-CN"));
});

function valueAtTime(game, targetTime) {
  let previous = null;
  for (const point of game.points) {
    if (point.time === targetTime) return point.value;
    if (point.time > targetTime) {
      if (!previous || previous.value === null || point.value === null) return null;
      const ratio = (targetTime - previous.time) / (point.time - previous.time);
      return previous.value + (point.value - previous.value) * ratio;
    }
    previous = point;
  }
  return null;
}

function formatCount(value) {
  return new Intl.NumberFormat("zh-CN").format(Math.round(Number(value) || 0));
}

function formatAxisValue(value) {
  if (value >= 10000) return (value / 10000).toFixed(value >= 100000 ? 0 : 1) + "万";
  return formatCount(value);
}

function formatDateTime(value) {
  if (!value) return "--";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function pointerPosition(event) {
  const rectangle = event.currentTarget.getBoundingClientRect();
  return {
    rectangle,
    x: (event.clientX - rectangle.left) / rectangle.width * chartWidth.value,
    y: (event.clientY - rectangle.top) / rectangle.height * chartHeight.value,
    localX: event.clientX - rectangle.left,
    localY: event.clientY - rectangle.top,
  };
}

function updateHover(event) {
  const position = pointerPosition(event);
  if (position.x < plot.left || position.x > chartWidth.value - plot.right || position.y < plot.top || position.y > chartHeight.value - plot.bottom) {
    hover.value = null;
    return;
  }
  const targetTime = visibleBounds.value.start
    + (position.x - plot.left) / plotWidth.value * (visibleBounds.value.end - visibleBounds.value.start);
  const times = [...new Set(normalizedGames.value.flatMap((game) => game.points
    .filter((point) => point.time >= visibleBounds.value.start && point.time <= visibleBounds.value.end)
    .map((point) => point.time)))];
  if (!times.length) {
    hover.value = null;
    return;
  }
  const time = times.reduce((closest, candidate) => (
    Math.abs(candidate - targetTime) < Math.abs(closest - targetTime) ? candidate : closest
  ));
  const hoveredGame = normalizedGames.value
    .map((game) => {
      const value = valueAtTime(game, targetTime);
      return {
        gameID: game.game_id,
        distance: value === null ? Number.POSITIVE_INFINITY : Math.abs(yForValue(value) - position.y),
      };
    })
    .sort((left, right) => left.distance - right.distance)[0];
  hover.value = {
    time,
    x: xForTime(time),
    gameID: hoveredGame?.distance <= 14 ? hoveredGame.gameID : "",
    localX: position.localX,
    localY: position.localY,
    width: position.rectangle.width,
  };
}

function setZoomRange(start, end) {
  const minimumSpan = 2;
  let nextStart = Math.max(0, Math.min(100 - minimumSpan, start));
  let nextEnd = Math.min(100, Math.max(minimumSpan, end));
  if (nextEnd - nextStart < minimumSpan) {
    const center = (nextStart + nextEnd) / 2;
    nextStart = Math.max(0, center - minimumSpan / 2);
    nextEnd = Math.min(100, nextStart + minimumSpan);
    nextStart = Math.max(0, nextEnd - minimumSpan);
  }
  zoomRange.value = [Number(nextStart.toFixed(2)), Number(nextEnd.toFixed(2))];
}

function zoomAt(center, factor) {
  const start = zoomRange.value[0];
  const end = zoomRange.value[1];
  const nextSpan = Math.max(2, Math.min(100, (end - start) * factor));
  const relative = end === start ? 0.5 : (center - start) / (end - start);
  setZoomRange(center - nextSpan * relative, center + nextSpan * (1 - relative));
}

function handleWheel(event) {
  if (!event.shiftKey) return;
  event.preventDefault();
  const position = pointerPosition(event);
  const center = zoomRange.value[0]
    + Math.max(0, Math.min(1, (position.x - plot.left) / plotWidth.value)) * (zoomRange.value[1] - zoomRange.value[0]);
  zoomAt(center, event.deltaY > 0 ? 1.18 : 0.84);
}

function pan(direction) {
  const span = zoomRange.value[1] - zoomRange.value[0];
  const offset = span * 0.12 * direction;
  setZoomRange(zoomRange.value[0] + offset, zoomRange.value[1] + offset);
}

function handleKeydown(event) {
  if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
    event.preventDefault();
    pan(event.key === "ArrowLeft" ? -1 : 1);
  } else if (event.key === "ArrowUp" || event.key === "ArrowDown") {
    event.preventDefault();
    zoomAt((zoomRange.value[0] + zoomRange.value[1]) / 2, event.key === "ArrowUp" ? 0.82 : 1.18);
  }
}

function jumpToTime() {
  const time = new Date(jumpAt.value).getTime();
  if (!Number.isFinite(time)) return;
  const ratio = (time - bounds.value.start) / (bounds.value.end - bounds.value.start) * 100;
  const span = Math.min(25, zoomRange.value[1] - zoomRange.value[0]);
  setZoomRange(ratio - span / 2, ratio + span / 2);
}

watch(() => [props.windowStart, props.windowEnd], () => {
  zoomRange.value = [0, 100];
  hover.value = null;
});

let resizeObserver;
onMounted(() => {
  resizeObserver = new ResizeObserver(([entry]) => {
    const rectangle = entry.contentRect;
    chartWidth.value = Math.max(280, Math.round(rectangle.width));
    chartHeight.value = Math.max(280, Math.round(rectangle.height));
  });
  if (chartCanvas.value) resizeObserver.observe(chartCanvas.value);
});
onBeforeUnmount(() => resizeObserver?.disconnect());
</script>

<template>
  <section class="series-chart">
    <header class="series-chart-head">
      <div>
        <h3>{{ title }}</h3>
        <p>{{ formatDateTime(visibleBounds.start) }} - {{ formatDateTime(visibleBounds.end) }}</p>
      </div>
      <div class="series-chart-controls">
        <input v-model="jumpAt" type="datetime-local" aria-label="定位时间" />
        <el-tooltip content="定位到指定时间">
          <el-button class="square-button" aria-label="定位到指定时间" @click="jumpToTime">
            <LocateFixed :size="16" />
          </el-button>
        </el-tooltip>
      </div>
    </header>

    <div v-if="normalizedGames.length" class="series-legend" aria-label="图例">
      <button
        v-for="game in normalizedGames"
        :key="'legend-' + game.game_id"
        type="button"
        class="series-legend-item"
        :title="`${game.name || game.game_id}：最新 ${formatCount(game.latest_online_count ?? 0)} 人`"
        @click="emit('open-game', game.game_id)"
      >
        <span :style="{ backgroundColor: game.color }"></span>
        <strong>{{ game.name || game.game_id }}</strong>
        <em>{{ formatCount(game.latest_online_count ?? 0) }}</em>
      </button>
    </div>

    <div
      ref="chartCanvas"
      class="series-chart-canvas"
      :class="{ empty: !normalizedGames.length }"
      tabindex="0"
      aria-label="Minecraft 服务器在线人数折线图"
      @mousemove="updateHover"
      @mouseleave="hover = null"
      @wheel="handleWheel"
      @keydown="handleKeydown"
    >
      <svg :viewBox="'0 0 ' + chartWidth + ' ' + chartHeight" preserveAspectRatio="none" role="img">
        <defs>
          <clipPath id="mc-server-series-clip">
            <rect :x="plot.left" :y="plot.top" :width="plotWidth" :height="plotHeight" />
          </clipPath>
        </defs>

        <g class="chart-grid">
          <line
            v-for="tick in yTicks"
            :key="'y-' + tick.y"
            :x1="plot.left"
            :x2="chartWidth - plot.right"
            :y1="tick.y"
            :y2="tick.y"
          />
          <line
            v-for="tick in xTicks"
            :key="'x-' + tick.x"
            :x1="tick.x"
            :x2="tick.x"
            :y1="plot.top"
            :y2="chartHeight - plot.bottom"
          />
        </g>

        <g class="axis-labels">
          <text
            v-for="tick in yTicks"
            :key="'yl-' + tick.y"
            :x="plot.left - 10"
            :y="tick.y + 4"
            text-anchor="end"
          >{{ formatAxisValue(tick.value) }}</text>
          <text
            v-for="tick in xTicks"
            :key="'xl-' + tick.x"
            :x="tick.x"
            :y="chartHeight - 25"
            text-anchor="middle"
          >{{ formatDateTime(tick.time) }}</text>
        </g>

        <g clip-path="url(#mc-server-series-clip)">
          <template v-for="game in renderedGames" :key="game.game_id">
            <polyline
              v-for="(path, index) in game.paths"
              :key="game.game_id + '-hit-' + index"
              :points="path"
              class="series-hit-line"
              @click.stop="emit('open-game', game.game_id)"
            />
            <polyline
              v-for="(path, index) in game.paths"
              :key="game.game_id + '-line-' + index"
              :points="path"
              :stroke="game.color"
              class="series-line"
            />
            <circle
              v-for="point in game.points.filter((item) => item.value !== null && item.time >= visibleBounds.start && item.time <= visibleBounds.end)"
              :key="game.game_id + '-' + point.time"
              :cx="xForTime(point.time)"
              :cy="yForValue(point.value)"
              r="8"
              class="series-hit-point"
              @click.stop="emit('open-game', game.game_id)"
            />
          </template>

          <line
            v-if="hover"
            :x1="hover.x"
            :x2="hover.x"
            :y1="plot.top"
            :y2="chartHeight - plot.bottom"
            class="hover-line"
          />
          <circle
            v-for="marker in hoverMarkers"
            :key="'marker-' + marker.gameID"
            :cx="marker.x"
            :cy="marker.y"
            r="4"
            :fill="marker.color"
            class="hover-marker"
            @click.stop="emit('open-game', marker.gameID)"
          />
        </g>
      </svg>

      <div v-if="!loading && !normalizedGames.length" class="chart-empty">
        <strong>暂无趋势数据</strong>
      </div>

      <div
        v-if="hover && hoverMarkers.length"
        class="chart-tooltip"
        :class="{ right: hover.localX > hover.width * 0.62 }"
        :style="{ left: hover.localX + 'px', top: hover.localY + 'px' }"
      >
        <time>{{ formatDateTime(hover.time) }}</time>
        <button
          v-for="marker in hoverMarkers"
          :key="'tooltip-' + marker.gameID"
          type="button"
          :class="{ active: marker.active }"
          @click="emit('open-game', marker.gameID)"
        >
          <span :style="{ backgroundColor: marker.color }"></span>
          <strong>{{ marker.name }}</strong>
          <em v-if="marker.active">当前悬浮</em>
          <b>{{ formatCount(marker.value) }}</b>
        </button>
      </div>
    </div>

    <footer class="series-zoom">
      <span>{{ formatDateTime(bounds.start) }}</span>
      <el-slider v-model="zoomRange" range :min="0" :max="100" :show-tooltip="false" />
      <span>{{ formatDateTime(bounds.end) }}</span>
    </footer>
  </section>
</template>

<style scoped>
.series-chart {
  min-width: 0;
  padding: 16px;
  border: 1px solid #dce2dd;
  border-radius: 6px;
  background: #fff;
}
.series-chart-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.series-chart-head h3 {
  margin: 0;
  font-size: 15px;
}
.series-chart-head p {
  margin: 4px 0 0;
  color: #6d756f;
  font-size: 12px;
}
.series-chart-controls {
  display: flex;
  align-items: center;
  gap: 8px;
}
.series-chart-controls input {
  width: 190px;
  height: 32px;
  padding: 0 10px;
  border: 1px solid #d5ddd7;
  border-radius: 5px;
  color: #26322b;
  background: #fff;
  font: inherit;
  font-size: 12px;
}
.series-chart-controls input:focus {
  outline: 2px solid rgba(57, 126, 175, 0.18);
  border-color: #397eaf;
}
.series-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 10px;
  margin-top: 10px;
}
.series-legend-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 9px;
  border: 1px solid #e2e7e3;
  border-radius: 12px;
  background: #fff;
  cursor: pointer;
}
.series-legend-item > span {
  width: 8px;
  height: 8px;
  flex: 0 0 auto;
  border-radius: 50%;
}
.series-legend-item strong {
  overflow: hidden;
  color: #44524a;
  font-size: 11px;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.series-legend-item em {
  flex: 0 0 auto;
  color: #7b857e;
  font-size: 10px;
  font-style: normal;
}
.series-legend-item:hover {
  border-color: #b9c6bd;
  background: #f6f9f7;
}
.chart-tooltip button {
  display: flex;
  align-items: center;
  min-width: 0;
  border: 0;
  color: #344139;
  background: transparent;
  cursor: pointer;
}
.chart-tooltip button > span {
  width: 9px;
  height: 9px;
  flex: 0 0 auto;
  border-radius: 50%;
}
.series-chart-canvas {
  position: relative;
  width: 100%;
  height: clamp(320px, 42vw, 520px);
  min-height: 320px;
  margin-top: 14px;
  overflow: hidden;
  outline: none;
  background: #fbfcfb;
}
.series-chart-canvas:focus-visible {
  box-shadow: inset 0 0 0 2px rgba(57, 126, 175, 0.22);
}
.series-chart-canvas svg {
  display: block;
  width: 100%;
  height: 100%;
}
.chart-grid line {
  stroke: #e3e8e4;
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}
.axis-labels {
  fill: #7a847d;
  font-size: 10px;
}
.series-line {
  fill: none;
  stroke-width: 2.2;
  stroke-linecap: round;
  stroke-linejoin: round;
  pointer-events: none;
  vector-effect: non-scaling-stroke;
}
.series-hit-line {
  fill: none;
  stroke: transparent;
  stroke-width: 14;
  cursor: pointer;
  vector-effect: non-scaling-stroke;
}
.series-hit-point {
  fill: transparent;
  cursor: pointer;
}
.hover-line {
  stroke: #68736b;
  stroke-width: 1;
  stroke-dasharray: 4 4;
  pointer-events: none;
  vector-effect: non-scaling-stroke;
}
.hover-marker {
  stroke: #fff;
  stroke-width: 2;
  cursor: pointer;
  vector-effect: non-scaling-stroke;
}
.chart-empty {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: #7b857e;
  font-size: 13px;
}
.chart-tooltip {
  position: absolute;
  z-index: 3;
  display: grid;
  gap: 5px;
  min-width: 190px;
  max-width: 280px;
  padding: 10px;
  border: 1px solid #d8dfda;
  border-radius: 5px;
  color: #28342d;
  background: rgba(255, 255, 255, 0.97);
  box-shadow: 0 5px 16px rgba(39, 52, 44, 0.12);
  pointer-events: auto;
  transform: translate(14px, 14px);
}
.chart-tooltip.right {
  transform: translate(calc(-100% - 14px), 14px);
}
.chart-tooltip time {
  padding-bottom: 5px;
  border-bottom: 1px solid #e3e8e4;
  color: #69746c;
  font-size: 11px;
}
.chart-tooltip button {
  width: 100%;
  gap: 7px;
  padding: 4px 5px;
  text-align: left;
}
.chart-tooltip button.active {
  color: #285f37;
  background: #edf6ef;
  box-shadow: inset 3px 0 #4f9960;
}
.chart-tooltip button strong {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
  font-weight: 500;
}
.chart-tooltip button em {
  flex: 0 0 auto;
  color: #3d8150;
  font-size: 9px;
  font-style: normal;
  font-weight: 700;
}
.chart-tooltip button b {
  flex: 0 0 auto;
  font-size: 11px;
}
.series-zoom {
  display: grid;
  grid-template-columns: auto minmax(120px, 1fr) auto;
  align-items: center;
  gap: 16px;
  margin-top: 8px;
  color: #7c867f;
  font-size: 10px;
}
.series-zoom :deep(.el-slider) {
  margin: 0;
}
@media (max-width: 720px) {
  .series-chart {
    padding: 12px;
  }
  .series-chart-head {
    align-items: flex-start;
    flex-direction: column;
  }
  .series-chart-controls {
    width: 100%;
  }
  .series-chart-controls input {
    flex: 1;
    width: auto;
  }
  .series-chart-canvas {
    height: 340px;
  }
  .series-zoom {
    grid-template-columns: 1fr;
    gap: 3px;
  }
  .series-zoom > span:last-child {
    text-align: right;
  }
}
</style>
