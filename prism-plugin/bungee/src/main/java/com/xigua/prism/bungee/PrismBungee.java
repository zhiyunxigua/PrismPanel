package com.xigua.prism.bungee;

import com.xigua.prism.core.PrismCore;
import net.md_5.bungee.api.plugin.Plugin;

public final class PrismBungee extends Plugin {
    private PrismCore core;

    @Override
    public void onEnable() {
        BungeeLogger logger = new BungeeLogger(getLogger());
        core = PrismCore.create(
                "bungee",
                logger,
                new BungeeScheduler(this),
                new BungeeTelemetry(getProxy()),
                new BungeeBackendRegistry(getProxy()),
                new BungeePlayerTransfer(getProxy())
        ).orElse(null);
        if (core == null) {
            getLogger().info("Prism daemon environment is unavailable; integration is disabled.");
            return;
        }
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
