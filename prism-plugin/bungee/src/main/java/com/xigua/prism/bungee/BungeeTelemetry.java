package com.xigua.prism.bungee;

import com.xigua.prism.core.TelemetryProvider;
import net.md_5.bungee.api.ProxyServer;
import net.md_5.bungee.api.connection.ProxiedPlayer;
import net.md_5.bungee.api.plugin.Plugin;
import net.md_5.bungee.api.plugin.PluginDescription;

import java.lang.management.ManagementFactory;
import java.nio.file.Path;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;

final class BungeeTelemetry implements TelemetryProvider {
    private final ProxyServer proxy;
    private final Map<UUID, Instant> joinedAt = new ConcurrentHashMap<>();

    BungeeTelemetry(ProxyServer proxy) {
        this.proxy = proxy;
    }

    @Override
    public Map<String, Object> snapshot() {
        Runtime runtime = Runtime.getRuntime();
        Map<String, Object> snapshot = new LinkedHashMap<>();
        snapshot.put("online_players", proxy.getOnlineCount());
        snapshot.put("max_players", proxy.getConfig().getPlayerLimit());
        snapshot.put("jvm_heap_used_bytes", runtime.totalMemory() - runtime.freeMemory());
        snapshot.put("jvm_heap_max_bytes", runtime.maxMemory());
        snapshot.put("jvm_threads", ManagementFactory.getThreadMXBean().getThreadCount());
        snapshot.put("players", players());
        snapshot.put("plugins", plugins());
        return snapshot;
    }

    private List<Map<String, Object>> players() {
        List<ProxiedPlayer> players = new ArrayList<>(proxy.getPlayers());
        players.sort(Comparator.comparing(ProxiedPlayer::getName, String.CASE_INSENSITIVE_ORDER));
        Instant now = Instant.now();
        joinedAt.keySet().retainAll(players.stream().map(ProxiedPlayer::getUniqueId).toList());
        List<Map<String, Object>> result = new ArrayList<>(players.size());
        for (ProxiedPlayer player : players) {
            Map<String, Object> item = new LinkedHashMap<>();
            item.put("uuid", player.getUniqueId().toString());
            item.put("name", player.getName());
            item.put("ping", player.getPing());
            item.put("joined_at", joinedAt.computeIfAbsent(player.getUniqueId(), ignored -> now).toString());
            if (player.getServer() != null) {
                item.put("server_id", player.getServer().getInfo().getName());
            }
            result.add(item);
        }
        return result;
    }

    private List<Map<String, Object>> plugins() {
        List<Plugin> plugins = new ArrayList<>(proxy.getPluginManager().getPlugins());
        plugins.sort(Comparator.comparing(item -> item.getDescription().getName(), String.CASE_INSENSITIVE_ORDER));
        List<Map<String, Object>> result = new ArrayList<>(plugins.size());
        for (Plugin plugin : plugins) {
            PluginDescription description = plugin.getDescription();
            Map<String, Object> item = new LinkedHashMap<>();
            item.put("name", description.getName());
            item.put("version", description.getVersion());
            item.put("main", description.getMain());
            item.put("authors", description.getAuthor() == null ? List.of() : List.of(description.getAuthor()));
            item.put("enabled", true);
            Path fileName = plugin.getFile().toPath().toAbsolutePath().normalize().getFileName();
            if (fileName != null) {
                item.put("source_file", fileName.toString());
            }
            result.add(item);
        }
        return result;
    }
}
