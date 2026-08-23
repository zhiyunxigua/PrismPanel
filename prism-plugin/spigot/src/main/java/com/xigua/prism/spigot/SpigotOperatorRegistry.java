package com.xigua.prism.spigot;

import com.xigua.prism.core.ManagedOperator;
import com.xigua.prism.core.OperatorApplyResult;
import com.xigua.prism.core.OperatorCommands;
import com.xigua.prism.core.OperatorDriftReport;
import com.xigua.prism.core.OperatorRegistry;
import com.xigua.prism.core.PrismCommandException;
import org.bukkit.Bukkit;
import org.bukkit.OfflinePlayer;
import org.bukkit.event.EventHandler;
import org.bukkit.event.EventPriority;
import org.bukkit.event.Listener;
import org.bukkit.event.player.PlayerCommandPreprocessEvent;
import org.bukkit.event.server.ServerCommandEvent;
import org.bukkit.plugin.java.JavaPlugin;
import org.bukkit.scheduler.BukkitTask;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.function.Consumer;

final class SpigotOperatorRegistry implements OperatorRegistry, Listener {
    private final JavaPlugin plugin;
    private final SpigotLogger logger;
    private Map<UUID, ManagedOperator> desired = Map.of();
    private Consumer<OperatorDriftReport> driftReporter = ignored -> { };
    private BukkitTask reconcileTask;
    private long revision = -1;
    private boolean active;

    SpigotOperatorRegistry(JavaPlugin plugin, SpigotLogger logger) {
        this.plugin = plugin;
        this.logger = logger;
    }

    void start() {
        if (reconcileTask != null) {
            return;
        }
        reconcileTask = Bukkit.getScheduler().runTaskTimer(plugin, this::reconcileDrift, 100L, 100L);
    }

    @Override
    public OperatorApplyResult replace(long revision, boolean active, List<ManagedOperator> operators) {
        Map<UUID, ManagedOperator> next = new HashMap<>();
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
        this.desired = Map.copyOf(next);
        if (!active) {
            return new OperatorApplyResult(revision, 0, 0);
        }
        DriftChanges changes = reconcile();
        return new OperatorApplyResult(revision, changes.restored().size(), changes.removed().size());
    }

    @Override
    public void setDriftReporter(Consumer<OperatorDriftReport> reporter) {
        driftReporter = reporter == null ? ignored -> { } : reporter;
    }

    @EventHandler(priority = EventPriority.LOWEST, ignoreCancelled = true)
    public void onPlayerCommand(PlayerCommandPreprocessEvent event) {
        if (!active || !OperatorCommands.isRestricted(event.getMessage())) {
            return;
        }
        event.setCancelled(true);
        event.getPlayer().sendMessage("OP is managed by PrismPanel.");
    }

    @EventHandler(priority = EventPriority.LOWEST, ignoreCancelled = true)
    public void onServerCommand(ServerCommandEvent event) {
        if (!active || !OperatorCommands.isRestricted(event.getCommand())) {
            return;
        }
        event.setCancelled(true);
        logger.warn("Blocked an operator command because OP is managed by PrismPanel.");
    }

    private void reconcileDrift() {
        if (!active) {
            return;
        }
        DriftChanges changes = reconcile();
        if (changes.restored().isEmpty() && changes.removed().isEmpty()) {
            return;
        }
        logger.warn("Corrected Minecraft operator state drift.");
        driftReporter.accept(new OperatorDriftReport(revision, changes.restored(), changes.removed()));
    }

    private DriftChanges reconcile() {
        Map<UUID, OfflinePlayer> actual = new HashMap<>();
        for (OfflinePlayer player : Bukkit.getOperators()) {
            actual.put(player.getUniqueId(), player);
        }
        List<String> restored = new ArrayList<>();
        for (UUID uuid : desired.keySet()) {
            if (actual.containsKey(uuid)) {
                continue;
            }
            Bukkit.getOfflinePlayer(uuid).setOp(true);
            restored.add(uuid.toString());
        }
        List<String> removed = new ArrayList<>();
        for (Map.Entry<UUID, OfflinePlayer> entry : actual.entrySet()) {
            if (desired.containsKey(entry.getKey())) {
                continue;
            }
            entry.getValue().setOp(false);
            removed.add(entry.getKey().toString());
        }
        restored.sort(String::compareTo);
        removed.sort(String::compareTo);
        return new DriftChanges(List.copyOf(restored), List.copyOf(removed));
    }

    @Override
    public void close() {
        if (reconcileTask != null) {
            reconcileTask.cancel();
            reconcileTask = null;
        }
        active = false;
        desired = Map.of();
    }

    private record DriftChanges(List<String> restored, List<String> removed) {
    }
}
