package com.xigua.prism.bungee;

import com.xigua.prism.core.BackendApplyResult;
import com.xigua.prism.core.BackendCatalog;
import com.xigua.prism.core.BackendServer;
import com.xigua.prism.core.PrismCommandException;
import com.xigua.prism.core.ProxyBackendRegistry;
import net.md_5.bungee.api.ProxyServer;
import net.md_5.bungee.api.config.ServerInfo;

import java.net.InetSocketAddress;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;

final class BungeeBackendRegistry implements ProxyBackendRegistry {
    private final ProxyServer proxy;
    private final Set<String> managedServerIds = new HashSet<>();
    private Map<String, String> currentCatalog = Map.of();
    private long currentRevision = -1;

    BungeeBackendRegistry(ProxyServer proxy) {
        this.proxy = proxy;
    }

    @Override
    public synchronized BackendApplyResult replace(BackendCatalog catalog) {
        Map<String, String> desired = validate(catalog);
        if (catalog.revision() < currentRevision) {
            throw new PrismCommandException("STALE_REVISION", "backend catalog revision is stale");
        }
        if (catalog.revision() == currentRevision) {
            if (desired.equals(currentCatalog)) {
                return new BackendApplyResult(currentRevision, 0, 0);
            }
            throw new PrismCommandException("REVISION_CONFLICT", "backend catalog revision was reused");
        }

        int removed = 0;
        for (String existingId : Set.copyOf(managedServerIds)) {
            if (!desired.containsKey(existingId) && proxy.getServers().remove(existingId) != null) {
                removed++;
            }
            if (!desired.containsKey(existingId)) {
                managedServerIds.remove(existingId);
            }
        }

        int applied = 0;
        for (Map.Entry<String, String> entry : desired.entrySet()) {
            InetSocketAddress address = new BackendServer(entry.getKey(), entry.getValue()).socketAddress();
            ServerInfo existing = proxy.getServers().get(entry.getKey());
            if (existing != null && existing.getAddress().equals(address)) {
                managedServerIds.add(entry.getKey());
                continue;
            }
            String motd = existing == null ? "" : existing.getMotd();
            boolean restricted = existing != null && existing.isRestricted();
            proxy.getServers().put(
                    entry.getKey(),
                    proxy.constructServerInfo(entry.getKey(), address, motd, restricted)
            );
            managedServerIds.add(entry.getKey());
            applied++;
        }

        currentCatalog = Map.copyOf(desired);
        currentRevision = catalog.revision();
        return new BackendApplyResult(currentRevision, applied, removed);
    }

    private Map<String, String> validate(BackendCatalog catalog) {
        Map<String, String> result = new LinkedHashMap<>();
        for (BackendServer server : catalog.servers()) {
            if (result.putIfAbsent(server.id(), server.address()) != null) {
                throw new PrismCommandException("DUPLICATE_SERVER_ID", "duplicate backend server: " + server.id());
            }
            server.socketAddress();
        }
        return result;
    }
}
