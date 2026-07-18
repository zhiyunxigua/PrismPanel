package com.xigua.prism.spigot;

import com.xigua.prism.core.PrismCore;
import org.bukkit.Bukkit;
import org.bukkit.plugin.java.JavaPlugin;

public final class PrismSpigot extends JavaPlugin {
    private PrismCore core;

    @Override
    public void onEnable() {
        SpigotLogger logger = new SpigotLogger(getLogger());
        SpigotTelemetry telemetry = new SpigotTelemetry();
        core = PrismCore.create(
                "spigot",
                logger,
                new SpigotScheduler(this),
                telemetry,
                null,
                null
        ).orElse(null);
        if (core == null) {
            getLogger().info("Prism daemon environment is unavailable; integration is disabled.");
            return;
        }
        Bukkit.getPluginManager().registerEvents(telemetry, this);
        core.start();
    }

    @Override
    public void onDisable() {
        if (core != null) {
            core.close();
            core = null;
        }
    }
}
