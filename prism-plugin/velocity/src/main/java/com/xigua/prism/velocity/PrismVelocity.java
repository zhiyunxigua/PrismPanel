package com.xigua.prism.velocity;

import com.google.inject.Inject;
import com.velocitypowered.api.event.Subscribe;
import com.velocitypowered.api.event.proxy.ProxyInitializeEvent;
import com.velocitypowered.api.event.proxy.ProxyShutdownEvent;
import com.velocitypowered.api.plugin.Plugin;
import com.velocitypowered.api.proxy.ProxyServer;
import com.xigua.prism.core.PrismCore;
import org.slf4j.Logger;

@Plugin(id = "prism", name = "Prism", version = "0.2.0", authors = {"xigua"})
public final class PrismVelocity {
    private final ProxyServer proxy;
    private final Logger platformLogger;
    private PrismCore core;

    @Inject
    public PrismVelocity(ProxyServer proxy, Logger logger) {
        this.proxy = proxy;
        this.platformLogger = logger;
    }

    @Subscribe
    public void onProxyInitialize(ProxyInitializeEvent event) {
        VelocityLogger logger = new VelocityLogger(platformLogger);
        core = PrismCore.create(
                "velocity",
                logger,
                new VelocityScheduler(proxy, this),
                new VelocityTelemetry(proxy),
                new VelocityBackendRegistry(proxy),
                new VelocityPlayerTransfer(proxy)
        ).orElse(null);
        if (core == null) {
            platformLogger.info("Prism daemon environment is unavailable; integration is disabled.");
            return;
        }
        core.start();
    }

    @Subscribe
    public void onProxyShutdown(ProxyShutdownEvent event) {
        if (core != null) {
            core.close();
            core = null;
        }
    }
}
