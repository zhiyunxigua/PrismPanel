package com.xigua.prism.fabric;

import com.google.gson.Gson;
import com.google.gson.GsonBuilder;
import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import com.google.gson.JsonSyntaxException;
import com.xigua.prism.core.ManagedOperator;
import com.xigua.prism.core.OperatorApplyResult;
import com.xigua.prism.core.OperatorDriftReport;
import com.xigua.prism.core.OperatorRegistry;
import com.xigua.prism.core.PlatformScheduler;
import com.xigua.prism.core.PrismCommandException;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.function.Consumer;

/**
 * Fabric 侧全服 OP 注册器：直接读写服务端 {@code ops.json}（vanilla 现代格式，
 * JSON 数组 {@code [{"uuid","name","level","bypassesPlayerLimit"}]}），
 * 语义对齐 {@code SpigotOperatorRegistry}：
 *
 * <ul>
 *   <li>{@code replace} 的 revision 单调性检查（stale / conflict 抛
 *       {@link PrismCommandException}，code 与 Spigot 一致）；</li>
 *   <li>{@code active=false} 只记录 desired 不写文件；</li>
 *   <li>{@code active=true} 时 reconcile ops.json：desired 缺失的补写
 *       （uuid/name/level=4/bypassesPlayerLimit=false）、当前名单中不在 desired 的移除；
 *       desired 中 name 与已有条目不同时更新；</li>
 *   <li>写文件用原子写（同目录临时文件 + rename），UTF-8；</li>
 *   <li>每 5 秒漂移检查（{@link PlatformScheduler#repeat}），有变化时上报
 *       {@link OperatorDriftReport}；</li>
 *   <li>{@code close()} 停止定时任务并清空 desired / active。</li>
 * </ul>
 *
 * <p>文件不存在视为空名单；文件存在但无法解析（旧对象格式 / 损坏 JSON）时跳过
 * 本轮 reconcile（不删除任何已有 OP），仅记录告警。所有状态变更在
 * {@code this} 上同步——{@code replace} 与漂移任务都经由同一个单线程
 * {@link FabricScheduler} 执行，{@code close} 来自关闭钩子线程，加锁保证可见性。
 */
final class FabricOperatorRegistry implements OperatorRegistry {
    private static final Gson GSON = new GsonBuilder().setPrettyPrinting().create();
    private static final Duration DRIFT_INTERVAL = Duration.ofSeconds(5);
    private static final int OP_LEVEL = 4;
    private static final boolean BYPASSES_PLAYER_LIMIT = false;

    private final Path opsFilePath;
    private final PlatformScheduler scheduler;
    private final FabricLogger logger;
    private Map<UUID, ManagedOperator> desired = Map.of();
    private Consumer<OperatorDriftReport> driftReporter = ignored -> { };
    private AutoCloseable reconcileTask;
    private long revision = -1;
    private boolean active;

    FabricOperatorRegistry(Path opsFilePath, PlatformScheduler scheduler, FabricLogger logger) {
        this.opsFilePath = opsFilePath;
        this.scheduler = scheduler;
        this.logger = logger;
    }

    void start() {
        if (reconcileTask != null) {
            return;
        }
        reconcileTask = scheduler.repeat(DRIFT_INTERVAL, DRIFT_INTERVAL, this::reconcileDrift);
    }

    @Override
    public synchronized OperatorApplyResult replace(
            long revision, boolean active, List<ManagedOperator> operators
    ) {
        Map<UUID, ManagedOperator> next = new LinkedHashMap<>();
        for (ManagedOperator operator : operators) {
            if (next.put(operator.uuid(), operator) != null) {
                throw new PrismCommandException("INVALID_REQUEST", "operator UUID is duplicated");
            }
        }
        if (revision < this.revision) {
            throw new PrismCommandException("STALE_REVISION", "operator catalog revision is stale");
        }
        if (revision == this.revision) {
            if (this.active != active || !desired.equals(next)) {
                throw new PrismCommandException("REVISION_CONFLICT", "operator catalog revision was reused");
            }
            return new OperatorApplyResult(revision, 0, 0);
        }
        this.revision = revision;
        this.active = active;
        this.desired = Collections.unmodifiableMap(new LinkedHashMap<>(next));
        if (!active) {
            return new OperatorApplyResult(revision, 0, 0);
        }
        try {
            DriftChanges changes = reconcile();
            return new OperatorApplyResult(revision, changes.restored().size(), changes.removed().size());
        } catch (IOException error) {
            logger.error("Failed to reconcile " + opsFilePath.getFileName() + ".", error);
            throw new PrismCommandException(
                    "OPERATORS_APPLY_FAILED",
                    "could not reconcile " + opsFilePath.getFileName() + ": " + error.getMessage()
            );
        }
    }

    @Override
    public void setDriftReporter(Consumer<OperatorDriftReport> reporter) {
        driftReporter = reporter == null ? ignored -> { } : reporter;
    }

    synchronized void reconcileDrift() {
        if (!active) {
            return;
        }
        try {
            DriftChanges changes = reconcile();
            if (changes.restored().isEmpty() && changes.removed().isEmpty()) {
                return;
            }
            logger.warn("Corrected Minecraft operator state drift.");
            driftReporter.accept(new OperatorDriftReport(revision, changes.restored(), changes.removed()));
        } catch (IOException | RuntimeException error) {
            // 漂移任务失败只记日志，避免异常终止定期任务。
            logger.error("Operator drift check failed.", error);
        }
    }

    private DriftChanges reconcile() throws IOException {
        List<JsonElement> raw = readOpsFile();
        if (raw == null) {
            return DriftChanges.none();
        }
        Map<UUID, OpsEntry> actualByUuid = new LinkedHashMap<>();
        for (JsonElement element : raw) {
            OpsEntry entry = parseEntry(element);
            if (entry != null) {
                actualByUuid.put(entry.uuid(), entry);
            }
        }
        List<String> restored = new ArrayList<>();
        List<String> removed = new ArrayList<>();
        boolean changed = false;
        for (ManagedOperator operator : desired.values()) {
            OpsEntry existing = actualByUuid.get(operator.uuid());
            if (existing == null) {
                restored.add(operator.uuid().toString());
                changed = true;
            } else if (!name(operator).isEmpty() && !name(operator).equals(existing.name())) {
                existing.raw().addProperty("name", name(operator));
                changed = true;
            }
        }
        for (OpsEntry entry : actualByUuid.values()) {
            if (!desired.containsKey(entry.uuid())) {
                removed.add(entry.uuid().toString());
                changed = true;
            }
        }
        restored.sort(Comparator.naturalOrder());
        removed.sort(Comparator.naturalOrder());
        if (changed) {
            writeOpsFile(mergedJson(raw, actualByUuid, desired));
        }
        return new DriftChanges(List.copyOf(restored), List.copyOf(removed));
    }

    /**
     * 读取 ops.json。文件不存在返回空名单；存在但无法解析（非 JSON 数组 / 损坏）
     * 返回 {@code null} 表示本轮跳过（不写文件、不误删已有 OP）。
     * 返回的列表按文件顺序包含全部数组元素（含无法解析的条目——
     * uuid 缺失/非字符串/格式非法的条目在写盘时原样保留，见 {@link #mergedJson}）。
     */
    private List<JsonElement> readOpsFile() throws IOException {
        if (!Files.exists(opsFilePath)) {
            return List.of();
        }
        String contents = Files.readString(opsFilePath, StandardCharsets.UTF_8);
        if (contents.isBlank()) {
            return List.of();
        }
        JsonElement root;
        try {
            root = JsonParser.parseString(contents);
        } catch (JsonSyntaxException error) {
            logger.warn("Ignoring unreadable " + opsFilePath.getFileName() + " (invalid JSON); "
                    + "operator reconciliation is skipped this round.");
            return null;
        }
        if (!root.isJsonArray()) {
            logger.warn("Ignoring " + opsFilePath.getFileName() + " (expected a JSON array); "
                    + "operator reconciliation is skipped this round.");
            return null;
        }
        List<JsonElement> elements = new ArrayList<>();
        for (JsonElement element : root.getAsJsonArray()) {
            if (element != null) {
                elements.add(element);
            }
        }
        return elements;
    }

    /** 把数组元素解析为 OP 条目；uuid 缺失/非字符串/格式非法时返回 {@code null}。 */
    private static OpsEntry parseEntry(JsonElement element) {
        if (element == null || !element.isJsonObject()) {
            return null;
        }
        JsonObject object = element.getAsJsonObject();
        JsonElement uuidElement = object.get("uuid");
        if (uuidElement == null || !uuidElement.isJsonPrimitive()) {
            return null;
        }
        UUID uuid;
        try {
            uuid = UUID.fromString(uuidElement.getAsString());
        } catch (IllegalArgumentException error) {
            return null;
        }
        String existingName = object.has("name") && object.get("name").isJsonPrimitive()
                ? object.get("name").getAsString() : "";
        return new OpsEntry(uuid, existingName, object);
    }

    /**
     * 原子写 ops.json：先写同目录临时文件，再 rename（优先 ATOMIC_MOVE，
     * 不支持时回退 REPLACE_EXISTING），避免半写文件被服务端读到。
     */
    private void writeOpsFile(JsonArray array) throws IOException {
        Path absolute = opsFilePath.toAbsolutePath();
        Path parent = absolute.getParent();
        if (parent == null) {
            throw new IOException("ops file has no parent directory: " + absolute);
        }
        Path temporary = Files.createTempFile(parent, "." + absolute.getFileName(), ".tmp");
        boolean moved = false;
        try {
            Files.writeString(temporary, GSON.toJson(array), StandardCharsets.UTF_8);
            try {
                Files.move(temporary, absolute, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING);
            } catch (AtomicMoveNotSupportedException error) {
                Files.move(temporary, absolute, StandardCopyOption.REPLACE_EXISTING);
            }
            moved = true;
        } finally {
            if (!moved) {
                Files.deleteIfExists(temporary);
            }
        }
    }

    /**
     * 按文件顺序重建 ops.json：desired 中的合法条目保留（name 已原地更新）、
     * 不在 desired 的合法条目移除、无法解析的条目（uuid 缺失/非法）原样保留
     * （不删除我们无法理解的内容），最后追加 desired 中缺失的新条目。
     */
    private JsonArray mergedJson(
            List<JsonElement> raw,
            Map<UUID, OpsEntry> actualByUuid,
            Map<UUID, ManagedOperator> desired
    ) {
        JsonArray array = new JsonArray();
        for (JsonElement element : raw) {
            OpsEntry entry = parseEntry(element);
            if (entry != null && !desired.containsKey(entry.uuid())) {
                continue;
            }
            array.add(element);
        }
        for (ManagedOperator operator : desired.values()) {
            if (actualByUuid.containsKey(operator.uuid())) {
                continue;
            }
            JsonObject object = new JsonObject();
            object.addProperty("uuid", operator.uuid().toString());
            object.addProperty("name", name(operator));
            object.addProperty("level", OP_LEVEL);
            object.addProperty("bypassesPlayerLimit", BYPASSES_PLAYER_LIMIT);
            array.add(object);
        }
        return array;
    }

    private static String name(ManagedOperator operator) {
        return operator.name() == null ? "" : operator.name();
    }

    @Override
    public synchronized void close() {
        if (reconcileTask != null) {
            try {
                reconcileTask.close();
            } catch (Exception ignored) {
            }
            reconcileTask = null;
        }
        active = false;
        desired = Map.of();
    }

    private record OpsEntry(UUID uuid, String name, JsonObject raw) {
    }

    private record DriftChanges(List<String> restored, List<String> removed) {
        static DriftChanges none() {
            return new DriftChanges(List.of(), List.of());
        }
    }
}
