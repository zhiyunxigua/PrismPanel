package com.xigua.prism.spigot;

import com.xigua.prism.core.TelemetryProvider;
import org.bukkit.Bukkit;
import org.bukkit.entity.Player;
import org.bukkit.event.EventHandler;
import org.bukkit.event.EventPriority;
import org.bukkit.event.Listener;
import org.bukkit.event.player.PlayerJoinEvent;
import org.bukkit.event.player.PlayerQuitEvent;
import org.bukkit.plugin.Plugin;

import java.lang.management.ManagementFactory;
import java.lang.reflect.Method;
import java.net.URISyntaxException;
import java.nio.file.Path;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;

final class SpigotTelemetry implements TelemetryProvider, Listener {
    private final Map<UUID, Instant> joinedAt = new ConcurrentHashMap<>();

    SpigotTelemetry() {
        Instant now = Instant.now();
        for (Player player : Bukkit.getOnlinePlayers()) {
            joinedAt.put(player.getUniqueId(), now);
        }
    }

    @EventHandler(priority = EventPriority.MONITOR)
    public void onPlayerJoin(PlayerJoinEvent event) {
        joinedAt.put(event.getPlayer().getUniqueId(), Instant.now());
    }

    @EventHandler(priority = EventPriority.MONITOR)
    public void onPlayerQuit(PlayerQuitEvent event) {
        joinedAt.remove(event.getPlayer().getUniqueId());
    }

    @Override
    public Map<String, Object> snapshot() {
        Map<String, Object> snapshot = commonSnapshot();
        Double tps = paperTps();
        Double mspt = paperMspt();
        if (tps != null) {
            snapshot.put("tps", tps);
        }
        if (mspt != null) {
            snapshot.put("mspt", mspt);
        }
        snapshot.put("online_players", Bukkit.getOnlinePlayers().size());
        snapshot.put("max_players", Bukkit.getMaxPlayers());
        snapshot.put("players", players());
        snapshot.put("plugins", plugins());
        return snapshot;
    }

    private Map<String, Object> commonSnapshot() {
        Runtime runtime = Runtime.getRuntime();
        Map<String, Object> snapshot = new LinkedHashMap<>();
        snapshot.put("jvm_heap_used_bytes", runtime.totalMemory() - runtime.freeMemory());
        snapshot.put("jvm_heap_max_bytes", runtime.maxMemory());
        snapshot.put("jvm_threads", ManagementFactory.getThreadMXBean().getThreadCount());
        return snapshot;
    }

    private List<Map<String, Object>> players() {
        List<Player> online = new ArrayList<>(Bukkit.getOnlinePlayers());
        online.sort(Comparator.comparing(Player::getName, String.CASE_INSENSITIVE_ORDER));
        List<Map<String, Object>> result = new ArrayList<>(online.size());
        for (Player player : online) {
            Map<String, Object> item = new LinkedHashMap<>();
            item.put("uuid", player.getUniqueId().toString());
            item.put("name", player.getName());
            item.put("ping", player.getPing());
            item.put("joined_at", joinedAt.computeIfAbsent(player.getUniqueId(), ignored -> Instant.now()).toString());
            result.add(item);
        }
        return result;
    }

    private List<Map<String, Object>> plugins() {
        List<Plugin> sorted = new ArrayList<>(List.of(Bukkit.getPluginManager().getPlugins()));
        sorted.sort(Comparator.comparing(Plugin::getName, String.CASE_INSENSITIVE_ORDER));
        List<Map<String, Object>> result = new ArrayList<>(sorted.size());
        for (Plugin plugin : sorted) {
            Map<String, Object> item = new LinkedHashMap<>();
            item.put("name", plugin.getName());
            item.put("version", plugin.getDescription().getVersion());
            item.put("main", plugin.getDescription().getMain());
            item.put("authors", plugin.getDescription().getAuthors());
            item.put("enabled", plugin.isEnabled());
            addSourceMetadata(plugin, item);
            result.add(item);
        }
        return result;
    }

    private void addSourceMetadata(Plugin plugin, Map<String, Object> item) {
        try {
            Path source = Path.of(plugin.getClass().getProtectionDomain().getCodeSource().getLocation().toURI())
                    .toAbsolutePath().normalize();
            if (source.getFileName() != null) {
                item.put("source_file", source.getFileName().toString());
            }
        } catch (URISyntaxException | RuntimeException ignored) {
        }
    }

    private Double paperTps() {
        try {
            Method method = Bukkit.class.getMethod("getTPS");
            Object value = method.invoke(null);
            if (value instanceof double[] samples && samples.length > 0) {
                return samples[0];
            }
        } catch (ReflectiveOperationException ignored) {
        }
        return null;
    }

    private Double paperMspt() {
        try {
            Method method = Bukkit.class.getMethod("getAverageTickTime");
            Object value = method.invoke(null);
            if (value instanceof Number number) {
                return number.doubleValue();
            }
        } catch (ReflectiveOperationException ignored) {
        }
        return null;
    }
}
