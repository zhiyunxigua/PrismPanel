package com.xigua.prism.fabric;

import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import com.xigua.prism.core.ManagedOperator;
import com.xigua.prism.core.OperatorApplyResult;
import com.xigua.prism.core.OperatorDriftReport;
import com.xigua.prism.core.PlatformScheduler;
import com.xigua.prism.core.PrismCommandException;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.Callable;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class FabricOperatorRegistryTest {
    private static final UUID UUID_A = UUID.fromString("123e4567-e89b-12d3-a456-426614174000");
    private static final UUID UUID_B = UUID.fromString("223e4567-e89b-12d3-a456-426614174000");
    private static final UUID UUID_C = UUID.fromString("323e4567-e89b-12d3-a456-426614174000");

    @TempDir
    Path tempDir;

    private Path opsPath() {
        return tempDir.resolve("ops.json");
    }

    private FabricOperatorRegistry registry() {
        return new FabricOperatorRegistry(opsPath(), new ManualScheduler(), new FabricLogger());
    }

    @Test
    void appliesDesiredOperatorsToMissingFile() throws Exception {
        FabricOperatorRegistry registry = registry();
        OperatorApplyResult result = registry.replace(1, true, List.of(
                new ManagedOperator(UUID_A, "Steve"), new ManagedOperator(UUID_B, "Alex")
        ));
        assertEquals(2, result.applied());
        assertEquals(0, result.removed());

        Map<String, JsonObject> ops = readOps();
        assertEquals(2, ops.size());
        assertOp(ops, UUID_A, "Steve");
        assertOp(ops, UUID_B, "Alex");
    }

    @Test
    void preservesExistingEntriesAndRestoresMissing() throws Exception {
        writeOps("[{\"uuid\":\"" + UUID_A + "\",\"name\":\"Steve\",\"level\":4,"
                + "\"bypassesPlayerLimit\":false,\"custom\":\"keep-me\"}]");
        FabricOperatorRegistry registry = registry();
        OperatorApplyResult result = registry.replace(1, true, List.of(
                new ManagedOperator(UUID_A, "Steve"), new ManagedOperator(UUID_B, "Alex")
        ));
        assertEquals(1, result.applied());
        assertEquals(0, result.removed());

        Map<String, JsonObject> ops = readOps();
        assertEquals(2, ops.size());
        assertOp(ops, UUID_A, "Steve");
        // 保留条目原样保留（含未知字段），不被重建。
        assertEquals("keep-me", ops.get(UUID_A.toString()).get("custom").getAsString());
        assertOp(ops, UUID_B, "Alex");
    }

    @Test
    void removesOperatorsNotInDesired() throws Exception {
        writeOps("[{\"uuid\":\"" + UUID_A + "\",\"name\":\"Steve\",\"level\":4,"
                + "\"bypassesPlayerLimit\":false},{\"uuid\":\"" + UUID_B
                + "\",\"name\":\"Alex\",\"level\":4,\"bypassesPlayerLimit\":false}]");
        FabricOperatorRegistry registry = registry();
        OperatorApplyResult result = registry.replace(1, true, List.of(new ManagedOperator(UUID_A, "Steve")));
        assertEquals(0, result.applied());
        assertEquals(1, result.removed());

        Map<String, JsonObject> ops = readOps();
        assertEquals(1, ops.size());
        assertOp(ops, UUID_A, "Steve");
        assertFalse(ops.containsKey(UUID_B.toString()));
    }

    @Test
    void rejectsStaleRevision() throws Exception {
        FabricOperatorRegistry registry = registry();
        registry.replace(2, true, List.of(new ManagedOperator(UUID_A, "Steve")));
        PrismCommandException error = assertThrows(PrismCommandException.class,
                () -> registry.replace(1, true, List.of(new ManagedOperator(UUID_A, "Steve"))));
        assertEquals("STALE_REVISION", error.code());
    }

    @Test
    void rejectsRevisionConflict() throws Exception {
        FabricOperatorRegistry registry = registry();
        registry.replace(1, true, List.of(new ManagedOperator(UUID_A, "Steve")));
        PrismCommandException error = assertThrows(PrismCommandException.class,
                () -> registry.replace(1, true, List.of(new ManagedOperator(UUID_B, "Alex"))));
        assertEquals("REVISION_CONFLICT", error.code());
    }

    @Test
    void acceptsIdempotentReplaceWithSameRevision() throws Exception {
        FabricOperatorRegistry registry = registry();
        registry.replace(1, true, List.of(new ManagedOperator(UUID_A, "Steve")));
        OperatorApplyResult result = registry.replace(1, true, List.of(new ManagedOperator(UUID_A, "Steve")));
        assertEquals(0, result.applied());
        assertEquals(0, result.removed());
    }

    @Test
    void rejectsDuplicatedOperatorUuid() throws Exception {
        FabricOperatorRegistry registry = registry();
        PrismCommandException error = assertThrows(PrismCommandException.class, () -> registry.replace(
                1, true, List.of(new ManagedOperator(UUID_A, "Steve"), new ManagedOperator(UUID_A, "Steve"))
        ));
        assertEquals("INVALID_REQUEST", error.code());
    }

    @Test
    void inactiveReplaceDoesNotTouchFile() throws Exception {
        writeOps("[{\"uuid\":\"" + UUID_A + "\",\"name\":\"Steve\",\"level\":4,"
                + "\"bypassesPlayerLimit\":false}]");
        String before = Files.readString(opsPath(), StandardCharsets.UTF_8);
        FabricOperatorRegistry registry = registry();
        OperatorApplyResult result = registry.replace(1, false, List.of(
                new ManagedOperator(UUID_A, "Steve"), new ManagedOperator(UUID_B, "Alex")
        ));
        assertEquals(0, result.applied());
        assertEquals(0, result.removed());
        assertEquals(before, Files.readString(opsPath(), StandardCharsets.UTF_8));
    }

    @Test
    void updatesNameOfExistingEntry() throws Exception {
        writeOps("[{\"uuid\":\"" + UUID_A + "\",\"name\":\"Steve\",\"level\":4,"
                + "\"bypassesPlayerLimit\":false}]");
        FabricOperatorRegistry registry = registry();
        OperatorApplyResult result = registry.replace(1, true, List.of(new ManagedOperator(UUID_A, "Steven")));
        // name 变更不计入 restored/removed。
        assertEquals(0, result.applied());
        assertEquals(0, result.removed());
        assertOp(readOps(), UUID_A, "Steven");
    }

    @Test
    void driftCheckRestoresAndRemoves() throws Exception {
        writeOps("[{\"uuid\":\"" + UUID_A + "\",\"name\":\"Steve\",\"level\":4,"
                + "\"bypassesPlayerLimit\":false}]");
        FabricOperatorRegistry registry = registry();
        registry.replace(1, true, List.of(new ManagedOperator(UUID_A, "Steve")));
        // 外部篡改：删掉 A、加上 C。
        writeOps("[{\"uuid\":\"" + UUID_C + "\",\"name\":\"Creeper\",\"level\":4,"
                + "\"bypassesPlayerLimit\":false}]");

        AtomicReference<OperatorDriftReport> report = new AtomicReference<>();
        registry.setDriftReporter(report::set);
        registry.reconcileDrift();

        OperatorDriftReport drift = report.get();
        assertNotNull(drift);
        assertEquals(1, drift.revision());
        assertEquals(List.of(UUID_A.toString()), drift.restored());
        assertEquals(List.of(UUID_C.toString()), drift.removed());
        Map<String, JsonObject> ops = readOps();
        assertEquals(1, ops.size());
        assertOp(ops, UUID_A, "Steve");
    }

    @Test
    void driftCheckDoesNotReportWhenClean() throws Exception {
        FabricOperatorRegistry registry = registry();
        registry.replace(1, true, List.of(new ManagedOperator(UUID_A, "Steve")));
        AtomicReference<OperatorDriftReport> report = new AtomicReference<>();
        registry.setDriftReporter(report::set);
        registry.reconcileDrift();
        assertNull(report.get());
    }

    @Test
    void preservesMalformedEntriesOnWrite() throws Exception {
        // 无 uuid 的条目与无连字符 uuid（32 hex）条目都无法解析为合法 OP，
        // 写盘时必须原样保留，不静默删除我们无法理解的内容。
        writeOps("[{\"name\":\"Ghost\",\"level\":4},"
                + "{\"uuid\":\"" + "123e4567e89b12d3a456426614174000" + "\",\"name\":\"Old\",\"level\":2},"
                + "{\"uuid\":\"" + UUID_A + "\",\"name\":\"Steve\",\"level\":4,"
                + "\"bypassesPlayerLimit\":false}]");
        FabricOperatorRegistry registry = registry();
        OperatorApplyResult result = registry.replace(1, true, List.of(new ManagedOperator(UUID_A, "Steve")));
        assertEquals(0, result.applied());
        assertEquals(0, result.removed());

        JsonArray array = JsonParser.parseString(Files.readString(opsPath(), StandardCharsets.UTF_8))
                .getAsJsonArray();
        assertEquals(3, array.size());
        // 无 uuid 条目原样保留。
        JsonObject ghost = array.get(0).getAsJsonObject();
        assertFalse(ghost.has("uuid"));
        assertEquals("Ghost", ghost.get("name").getAsString());
        // 无连字符 uuid 条目原样保留（不被当作合法 OP 删除，也不被规范化改写）。
        JsonObject old = array.get(1).getAsJsonObject();
        assertEquals("123e4567e89b12d3a456426614174000", old.get("uuid").getAsString());
        assertEquals(2, old.get("level").getAsInt());
        // 合法条目仍在。
        assertOp(readOps(), UUID_A, "Steve");
    }

    @Test
    void driftCheckIsNoopWhenInactive() throws Exception {
        writeOps("[{\"uuid\":\"" + UUID_A + "\",\"name\":\"Steve\",\"level\":4,"
                + "\"bypassesPlayerLimit\":false}]");
        String before = Files.readString(opsPath(), StandardCharsets.UTF_8);
        FabricOperatorRegistry registry = registry();
        registry.replace(1, false, List.of(new ManagedOperator(UUID_A, "Steve")));
        AtomicReference<OperatorDriftReport> report = new AtomicReference<>();
        registry.setDriftReporter(report::set);
        registry.reconcileDrift();
        assertNull(report.get());
        assertEquals(before, Files.readString(opsPath(), StandardCharsets.UTF_8));
    }

    @Test
    void skipsReconcileForUnreadableFileWithoutDeletingOps() throws Exception {
        writeOps("not-json-at-all");
        FabricOperatorRegistry registry = registry();
        OperatorApplyResult result = registry.replace(1, true, List.of(new ManagedOperator(UUID_A, "Steve")));
        // 无法解析时跳过本轮：不写文件、不误删已有内容。
        assertEquals(0, result.applied());
        assertEquals(0, result.removed());
        assertEquals("not-json-at-all", Files.readString(opsPath(), StandardCharsets.UTF_8));
    }

    @Test
    void skipsReconcileForOldObjectFormatWithoutDeletingOps() throws Exception {
        writeOps("{\"" + UUID_A + "\":{\"name\":\"Steve\",\"level\":4}}");
        FabricOperatorRegistry registry = registry();
        OperatorApplyResult result = registry.replace(1, true, List.of(new ManagedOperator(UUID_A, "Steve")));
        assertEquals(0, result.applied());
        assertEquals(0, result.removed());
        Map<String, JsonObject> ops = readOpsAsObject();
        assertTrue(ops.containsKey(UUID_A.toString()));
    }

    @Test
    void startSchedulesDriftTaskAndCloseStopsIt() throws Exception {
        ManualScheduler scheduler = new ManualScheduler();
        FabricOperatorRegistry registry = new FabricOperatorRegistry(opsPath(), scheduler, new FabricLogger());
        assertEquals(0, scheduler.repeatCount());
        registry.start();
        registry.start(); // 幂等
        assertEquals(1, scheduler.repeatCount());
        assertEquals(Duration.ofSeconds(5), scheduler.firstInterval());
        registry.close();
        assertEquals(1, scheduler.closedCount());
        // close 后漂移检查不再生效。
        registry.reconcileDrift();
        assertFalse(Files.exists(opsPath()));
    }

    @Test
    void atomicWriteLeavesNoPartialFile() throws Exception {
        FabricOperatorRegistry registry = registry();
        registry.replace(1, true, List.of(
                new ManagedOperator(UUID_A, "Steve"), new ManagedOperator(UUID_B, "Alex")
        ));
        assertOp(readOps(), UUID_A, "Steve");
        assertOp(readOps(), UUID_B, "Alex");
        try (var files = Files.list(tempDir)) {
            List<String> leftovers = files.map(path -> path.getFileName().toString())
                    .filter(name -> name.endsWith(".tmp"))
                    .toList();
            assertTrue(leftovers.isEmpty(), "temporary files left behind: " + leftovers);
        }
    }

    // ---- helpers ----

    private Map<String, JsonObject> readOps() throws IOException {
        String contents = Files.readString(opsPath(), StandardCharsets.UTF_8);
        JsonArray array = JsonParser.parseString(contents).getAsJsonArray();
        Map<String, JsonObject> result = new LinkedHashMap<>();
        for (JsonElement element : array) {
            JsonObject object = element.getAsJsonObject();
            JsonElement uuidElement = object.get("uuid");
            if (uuidElement == null || !uuidElement.isJsonPrimitive()) {
                continue;
            }
            try {
                UUID.fromString(uuidElement.getAsString());
            } catch (IllegalArgumentException error) {
                continue;
            }
            result.put(uuidElement.getAsString(), object);
        }
        return result;
    }

    private Map<String, JsonObject> readOpsAsObject() throws IOException {
        String contents = Files.readString(opsPath(), StandardCharsets.UTF_8);
        JsonObject object = JsonParser.parseString(contents).getAsJsonObject();
        Map<String, JsonObject> result = new LinkedHashMap<>();
        for (String key : object.keySet()) {
            result.put(key, object.getAsJsonObject(key));
        }
        return result;
    }

    private void assertOp(Map<String, JsonObject> ops, UUID uuid, String name) {
        JsonObject op = ops.get(uuid.toString());
        assertNotNull(op, "missing operator " + uuid);
        assertEquals(name, op.get("name").getAsString());
        assertEquals(4, op.get("level").getAsInt());
        assertFalse(op.get("bypassesPlayerLimit").getAsBoolean());
    }

    private void writeOps(String contents) throws IOException {
        Files.writeString(opsPath(), contents, StandardCharsets.UTF_8);
    }

    /** 不自动执行 repeat 任务的测试调度器：记录注册与关闭，供手动触发 reconcileDrift。 */
    private static final class ManualScheduler implements PlatformScheduler {
        private final List<AutoCloseable> tasks = new ArrayList<>();
        private final List<Boolean> closed = new ArrayList<>();
        private Duration firstInterval;

        @Override
        public <T> CompletableFuture<T> call(Callable<T> task) {
            CompletableFuture<T> result = new CompletableFuture<>();
            try {
                result.complete(task.call());
            } catch (Throwable error) {
                result.completeExceptionally(error);
            }
            return result;
        }

        @Override
        public AutoCloseable repeat(Duration initialDelay, Duration interval, Runnable task) {
            if (tasks.isEmpty()) {
                firstInterval = interval;
            }
            AutoCloseable handle = () -> closed.add(true);
            tasks.add(handle);
            return handle;
        }

        int repeatCount() {
            return tasks.size();
        }

        int closedCount() {
            return closed.size();
        }

        Duration firstInterval() {
            return firstInterval;
        }
    }
}
