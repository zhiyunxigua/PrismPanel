package com.xigua.prism.core;

import java.util.UUID;
import java.util.concurrent.CompletableFuture;

@FunctionalInterface
public interface PlayerTransferService {
    CompletableFuture<Void> transfer(UUID playerId, String targetServerId);
}
