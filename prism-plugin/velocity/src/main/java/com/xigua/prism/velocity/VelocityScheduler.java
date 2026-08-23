package com.xigua.prism.velocity;

import com.velocitypowered.api.proxy.ProxyServer;
import com.velocitypowered.api.scheduler.ScheduledTask;
import com.xigua.prism.core.PlatformScheduler;

import java.time.Duration;
import java.util.concurrent.Callable;
import java.util.concurrent.CompletableFuture;

final class VelocityScheduler implements PlatformScheduler {
    private final ProxyServer proxy;
    private final Object plugin;

    VelocityScheduler(ProxyServer proxy, Object plugin) {
        this.proxy = proxy;
        this.plugin = plugin;
    }

    @Override
    public <T> CompletableFuture<T> call(Callable<T> task) {
        CompletableFuture<T> result = new CompletableFuture<>();
        proxy.getScheduler().buildTask(plugin, () -> {
            try {
                result.complete(task.call());
            } catch (Throwable error) {
                result.completeExceptionally(error);
            }
        }).schedule();
        return result;
    }

    @Override
    public AutoCloseable repeat(Duration initialDelay, Duration interval, Runnable task) {
        ScheduledTask scheduled = proxy.getScheduler()
                .buildTask(plugin, task)
                .delay(initialDelay)
                .repeat(interval)
                .schedule();
        return scheduled::cancel;
    }
}
