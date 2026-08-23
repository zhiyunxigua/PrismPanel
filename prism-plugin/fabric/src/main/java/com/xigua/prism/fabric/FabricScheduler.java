package com.xigua.prism.fabric;

import com.xigua.prism.core.PlatformScheduler;

import java.time.Duration;
import java.util.concurrent.Callable;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.TimeUnit;

/**
 * Fabric 侧调度适配：基于单线程 ScheduledExecutorService。
 * 本 mod 不执行任何平台命令（proxy.backends 等），call() 仅做线程池化兜底。
 */
public final class FabricScheduler implements PlatformScheduler {
    private final ScheduledExecutorService executor = Executors.newSingleThreadScheduledExecutor(runnable -> {
        Thread thread = new Thread(runnable, "prism-fabric-scheduler");
        thread.setDaemon(true);
        return thread;
    });

    @Override
    public <T> CompletableFuture<T> call(Callable<T> task) {
        CompletableFuture<T> result = new CompletableFuture<>();
        executor.execute(() -> {
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
        ScheduledFuture<?> scheduled = executor.scheduleAtFixedRate(
                task, initialDelay.toMillis(), interval.toMillis(), TimeUnit.MILLISECONDS
        );
        return () -> scheduled.cancel(false);
    }
}
