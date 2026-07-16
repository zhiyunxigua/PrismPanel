package com.xigua.prismMC;

import com.xigua.prismMC.daemon.DaemonBridge;
import com.xigua.prismMC.telemetry.TelemetryCollector;
import org.bukkit.Bukkit;
import org.bukkit.plugin.java.JavaPlugin;
import org.bukkit.scheduler.BukkitTask;

public final class PrismMC extends JavaPlugin {
    private DaemonBridge bridge;
    private BukkitTask telemetryTask;

    @Override
    public void onEnable() {
        bridge = DaemonBridge.fromEnvironment(this);
        if (bridge == null) {
            getLogger().info("Prism daemon environment is unavailable; telemetry is disabled.");
            return;
        }
        TelemetryCollector collector = new TelemetryCollector();
        Bukkit.getPluginManager().registerEvents(collector, this);
        bridge.start();
        telemetryTask = Bukkit.getScheduler().runTaskTimer(
                this,
                () -> bridge.publishSnapshot(collector.snapshot()),
                100L,
                100L
        );
    }

    @Override
    public void onDisable() {
        if (telemetryTask != null) {
            telemetryTask.cancel();
        }
        if (bridge != null) {
            bridge.close();
        }
    }
}
