package com.xigua.prism.bungee;

import com.xigua.prism.core.PlatformScheduler;
import net.md_5.bungee.api.plugin.Plugin;
import net.md_5.bungee.api.scheduler.ScheduledTask;

import java.time.Duration;
import java.util.concurrent.Callable;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

final class BungeeScheduler implements PlatformScheduler {
    private final Plugin plugin;

    BungeeScheduler(Plugin plugin) {
        this.plugin = plugin;
    }

    @Override
    public <T> CompletableFuture<T> call(Callable<T> task) {
        CompletableFuture<T> result = new CompletableFuture<>();
        plugin.getProxy().getScheduler().runAsync(plugin, () -> {
            try {
                result.complete(task.call());
            } catch (Throwable error) {
                result.completeExceptionally(error);
            }
        });
        return result;
    }

    @Override
    public AutoCloseable repeat(Duration initialDelay, Duration interval, Runnable task) {
        ScheduledTask scheduled = plugin.getProxy().getScheduler().schedule(
                plugin,
                task,
                initialDelay.toMillis(),
                interval.toMillis(),
                TimeUnit.MILLISECONDS
        );
        return scheduled::cancel;
    }
}
