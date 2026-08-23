package com.xigua.prism.core;

import java.net.InetAddress;
import java.net.URI;
import java.util.Locale;
import java.util.Map;
import java.util.Optional;

public record PrismEnvironment(
        URI endpoint,
        String instanceId,
        String sessionId,
        String token,
        String platform
) {
    public static Optional<PrismEnvironment> fromSystem(PrismLogger logger, String platform) {
        return from(System.getenv(), logger, platform);
    }

    static Optional<PrismEnvironment> from(Map<String, String> environment, PrismLogger logger, String platform) {
        String endpoint = environment.get("PRISM_DAEMON_WS");
        String instanceId = environment.get("PRISM_INSTANCE_ID");
        String sessionId = environment.get("PRISM_SESSION_ID");
        String token = environment.get("PRISM_PLUGIN_TOKEN");
        if (isBlank(endpoint) || isBlank(instanceId) || isBlank(sessionId) || isBlank(token)) {
            return Optional.empty();
        }
        try {
            URI uri = URI.create(endpoint);
            if (!isLocalWebSocket(uri)) {
                logger.warn("Prism daemon endpoint must be a loopback WebSocket URL.");
                return Optional.empty();
            }
            return Optional.of(new PrismEnvironment(
                    uri,
                    instanceId,
                    sessionId,
                    token,
                    platform.toLowerCase(Locale.ROOT)
            ));
        } catch (RuntimeException error) {
            logger.warn("Prism daemon endpoint is invalid: " + error.getMessage());
            return Optional.empty();
        }
    }

    private static boolean isBlank(String value) {
        return value == null || value.isBlank();
    }

    private static boolean isLocalWebSocket(URI uri) {
        String scheme = uri.getScheme();
        if (!"ws".equalsIgnoreCase(scheme) && !"wss".equalsIgnoreCase(scheme)) {
            return false;
        }
        try {
            return uri.getHost() != null && InetAddress.getByName(uri.getHost()).isLoopbackAddress();
        } catch (Exception error) {
            return false;
        }
    }
}
