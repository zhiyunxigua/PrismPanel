package com.xigua.prism.core;

import java.time.Duration;
import java.util.concurrent.Callable;
import java.util.concurrent.CompletableFuture;

public interface PlatformScheduler {
    <T> CompletableFuture<T> call(Callable<T> task);

    AutoCloseable repeat(Duration initialDelay, Duration interval, Runnable task);
}
