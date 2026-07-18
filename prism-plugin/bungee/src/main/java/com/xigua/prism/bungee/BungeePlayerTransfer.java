package com.xigua.prism.bungee;

import com.xigua.prism.core.PlayerTransferService;
import com.xigua.prism.core.PrismCommandException;
import net.md_5.bungee.api.ProxyServer;
import net.md_5.bungee.api.config.ServerInfo;
import net.md_5.bungee.api.connection.ProxiedPlayer;

import java.util.UUID;
import java.util.concurrent.CompletableFuture;

final class BungeePlayerTransfer implements PlayerTransferService {
    private final ProxyServer proxy;

    BungeePlayerTransfer(ProxyServer proxy) {
        this.proxy = proxy;
    }

    @Override
    public CompletableFuture<Void> transfer(UUID playerId, String targetServerId) {
        ProxiedPlayer player = proxy.getPlayer(playerId);
        if (player == null) {
            throw new PrismCommandException("PLAYER_OFFLINE", "player is no longer connected");
        }
        ServerInfo target = proxy.getServerInfo(targetServerId);
        if (target == null) {
            throw new PrismCommandException("TARGET_NOT_FOUND", "target backend is not registered");
        }
        CompletableFuture<Void> result = new CompletableFuture<>();
        player.connect(target, (connected, error) -> {
            if (error != null) {
                result.completeExceptionally(error);
            } else if (!Boolean.TRUE.equals(connected)) {
                result.completeExceptionally(new PrismCommandException(
                        "TRANSFER_FAILED", "proxy rejected the player transfer"
                ));
            } else {
                result.complete(null);
            }
        });
        return result;
    }
}
