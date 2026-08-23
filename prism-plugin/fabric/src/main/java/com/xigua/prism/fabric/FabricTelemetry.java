package com.xigua.prism.fabric;

import com.xigua.prism.core.TelemetryProvider;

import net.fabricmc.loader.api.FabricLoader;
import net.fabricmc.loader.api.ModContainer;
import net.fabricmc.loader.api.metadata.ModMetadata;
import net.fabricmc.loader.api.metadata.ModOrigin;
import net.fabricmc.loader.api.metadata.Person;

import java.lang.management.ManagementFactory;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;

/**
 * Fabric 侧遥测：JVM 指标 + 服务端实际加载的 mod 列表。
 *
 * <p>mod 列表通过 {@link FabricLoader#getAllMods()} 采集，只上报
 * {@link ModOrigin.Kind#PATH} 来源（即直接放在 mods/ 目录的 jar，
 * fabric-loader 0.16.x 的 Kind 枚举为 PATH / NESTED / UNKNOWN），
 * 排除 NESTED（fabric-api 子模块、jar-in-jar 内嵌库）与内置 id
 * （minecraft / fabricloader / java），并排除自身（prism-fabric）。
 *
 * <p>上报元素与 daemon 的 LoadedPlugin 契约一致：
 * id / name / version / authors / enabled / source_file。
 */
public final class FabricTelemetry implements TelemetryProvider {
    private static final String SELF_ID = "prism-fabric";
    private static final Set<String> EXCLUDED_IDS = Set.of("minecraft", "fabricloader", "java");

    @Override
    public Map<String, Object> snapshot() {
        Map<String, Object> snapshot = new LinkedHashMap<>();
        Runtime runtime = Runtime.getRuntime();
        snapshot.put("jvm_heap_used_bytes", runtime.totalMemory() - runtime.freeMemory());
        snapshot.put("jvm_heap_max_bytes", runtime.maxMemory());
        snapshot.put("jvm_threads", ManagementFactory.getThreadMXBean().getThreadCount());
        snapshot.put("plugins", mods());
        return snapshot;
    }

    private List<Map<String, Object>> mods() {
        List<Map<String, Object>> result = new ArrayList<>();
        FabricLoader loader = FabricLoader.getInstance();
        for (ModContainer container : loader.getAllMods()) {
            if (container.getOrigin().getKind() != ModOrigin.Kind.PATH) {
                continue;
            }
            ModMetadata metadata = container.getMetadata();
            String id = metadata.getId();
            if (id == null || id.isBlank() || EXCLUDED_IDS.contains(id.toLowerCase(Locale.ROOT))
                    || SELF_ID.equals(id)) {
                continue;
            }
            Map<String, Object> item = new LinkedHashMap<>();
            item.put("id", id);
            item.put("name", metadata.getName());
            item.put("version", metadata.getVersion().getFriendlyString());
            List<String> authors = new ArrayList<>();
            for (Person author : metadata.getAuthors()) {
                authors.add(author.getName());
            }
            item.put("authors", authors);
            item.put("enabled", true);
            List<Path> paths = container.getOrigin().getPaths();
            if (paths != null && !paths.isEmpty() && paths.get(0) != null && paths.get(0).getFileName() != null) {
                item.put("source_file", paths.get(0).getFileName().toString());
            }
            result.add(item);
        }
        result.sort(Comparator.comparing(item -> String.valueOf(item.get("name")), String.CASE_INSENSITIVE_ORDER));
        return result;
    }
}
