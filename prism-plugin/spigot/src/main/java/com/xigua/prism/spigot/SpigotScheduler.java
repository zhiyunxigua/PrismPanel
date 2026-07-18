package com.xigua.prism.spigot;

import com.xigua.prism.core.PlatformScheduler;
import org.bukkit.Bukkit;
import org.bukkit.plugin.java.JavaPlugin;
import org.bukkit.scheduler.BukkitTask;

import java.time.Duration;
import java.util.concurrent.Callable;
import java.util.concurrent.CompletableFuture;

final class SpigotScheduler implements PlatformScheduler {
    private final JavaPlugin plugin;

    SpigotScheduler(JavaPlugin plugin) {
        this.plugin = plugin;
    }

    @Override
    public <T> CompletableFuture<T> call(Callable<T> task) {
        CompletableFuture<T> result = new CompletableFuture<>();
        Runnable operation = () -> {
            try {
                result.complete(task.call());
            } catch (Throwable error) {
                result.completeExceptionally(error);
            }
        };
        if (Bukkit.isPrimaryThread()) {
            operation.run();
        } else {
            Bukkit.getScheduler().runTask(plugin, operation);
        }
        return result;
    }

    @Override
    public AutoCloseable repeat(Duration initialDelay, Duration interval, Runnable task) {
        BukkitTask scheduled = Bukkit.getScheduler().runTaskTimer(
                plugin,
                task,
                toTicks(initialDelay),
                toTicks(interval)
        );
        return scheduled::cancel;
    }

    private static long toTicks(Duration duration) {
        return Math.max(1, duration.toMillis() / 50);
    }
}
