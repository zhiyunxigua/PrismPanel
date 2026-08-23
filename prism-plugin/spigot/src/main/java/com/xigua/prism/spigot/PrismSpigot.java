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
        SpigotOperatorRegistry operators = new SpigotOperatorRegistry(this, logger);
        core = PrismCore.create(
                "spigot",
                logger,
                new SpigotScheduler(this),
                telemetry,
                null,
                null,
                operators
        ).orElse(null);
        if (core == null) {
            operators.close();
            getLogger().info("Prism daemon environment is unavailable; integration is disabled.");
            return;
        }
        Bukkit.getPluginManager().registerEvents(telemetry, this);
        Bukkit.getPluginManager().registerEvents(operators, this);
        operators.start();
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
