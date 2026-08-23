package com.xigua.prism.velocity;

import com.velocitypowered.api.proxy.ConnectionRequestBuilder;
import com.velocitypowered.api.proxy.Player;
import com.velocitypowered.api.proxy.ProxyServer;
import com.velocitypowered.api.proxy.server.RegisteredServer;
import com.xigua.prism.core.PlayerTransferService;
import com.xigua.prism.core.PrismCommandException;

import java.util.UUID;
import java.util.concurrent.CompletableFuture;

final class VelocityPlayerTransfer implements PlayerTransferService {
    private final ProxyServer proxy;

    VelocityPlayerTransfer(ProxyServer proxy) {
        this.proxy = proxy;
    }

    @Override
    public CompletableFuture<Void> transfer(UUID playerId, String targetServerId) {
        Player player = proxy.getPlayer(playerId).orElseThrow(() -> new PrismCommandException(
                "PLAYER_OFFLINE", "player is no longer connected"
        ));
        RegisteredServer target = proxy.getServer(targetServerId).orElseThrow(() -> new PrismCommandException(
                "TARGET_NOT_FOUND", "target backend is not registered"
        ));
        return player.createConnectionRequest(target).connect().thenApply(result -> {
            if (!result.isSuccessful()) {
                ConnectionRequestBuilder.Status status = result.getStatus();
                throw new PrismCommandException("TRANSFER_FAILED", "player transfer failed: " + status.name());
            }
            return null;
        });
    }
}
