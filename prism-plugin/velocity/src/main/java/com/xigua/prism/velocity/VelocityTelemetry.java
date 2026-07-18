package com.xigua.prism.velocity;

import com.velocitypowered.api.plugin.PluginContainer;
import com.velocitypowered.api.plugin.PluginDescription;
import com.velocitypowered.api.proxy.Player;
import com.velocitypowered.api.proxy.ProxyServer;
import com.xigua.prism.core.FileFingerprintCache;
import com.xigua.prism.core.TelemetryProvider;

import java.lang.management.ManagementFactory;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;

final class VelocityTelemetry implements TelemetryProvider {
    private final ProxyServer proxy;
    private final Map<UUID, Instant> joinedAt = new ConcurrentHashMap<>();
    private final FileFingerprintCache fingerprints = new FileFingerprintCache();

    VelocityTelemetry(ProxyServer proxy) {
        this.proxy = proxy;
    }

    @Override
    public Map<String, Object> snapshot() {
        Runtime runtime = Runtime.getRuntime();
        Map<String, Object> snapshot = new LinkedHashMap<>();
        snapshot.put("online_players", proxy.getPlayerCount());
        snapshot.put("max_players", proxy.getConfiguration().getShowMaxPlayers());
        snapshot.put("jvm_heap_used_bytes", runtime.totalMemory() - runtime.freeMemory());
        snapshot.put("jvm_heap_max_bytes", runtime.maxMemory());
        snapshot.put("jvm_threads", ManagementFactory.getThreadMXBean().getThreadCount());
        snapshot.put("players", players());
        snapshot.put("plugins", plugins());
        return snapshot;
    }

    private List<Map<String, Object>> players() {
        List<Player> players = new ArrayList<>(proxy.getAllPlayers());
        players.sort(Comparator.comparing(Player::getUsername, String.CASE_INSENSITIVE_ORDER));
        Instant now = Instant.now();
        joinedAt.keySet().retainAll(players.stream().map(Player::getUniqueId).toList());
        List<Map<String, Object>> result = new ArrayList<>(players.size());
        for (Player player : players) {
            Map<String, Object> item = new LinkedHashMap<>();
            item.put("uuid", player.getUniqueId().toString());
            item.put("name", player.getUsername());
            item.put("ping", player.getPing());
            item.put("joined_at", joinedAt.computeIfAbsent(player.getUniqueId(), ignored -> now).toString());
            player.getCurrentServer().ifPresent(connection ->
                    item.put("server_id", connection.getServerInfo().getName())
            );
            result.add(item);
        }
        return result;
    }

    private List<Map<String, Object>> plugins() {
        List<PluginContainer> plugins = new ArrayList<>(proxy.getPluginManager().getPlugins());
        plugins.sort(Comparator.comparing(item -> item.getDescription().getId(), String.CASE_INSENSITIVE_ORDER));
        List<Map<String, Object>> result = new ArrayList<>(plugins.size());
        for (PluginContainer plugin : plugins) {
            PluginDescription description = plugin.getDescription();
            Map<String, Object> item = new LinkedHashMap<>();
            item.put("name", description.getName().orElse(description.getId()));
            item.put("version", description.getVersion().orElse(""));
            item.put("authors", description.getAuthors());
            item.put("enabled", true);
            description.getSource().ifPresent(source -> fingerprints.add(source, item));
            result.add(item);
        }
        return result;
    }
}
