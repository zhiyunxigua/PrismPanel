package com.xigua.prism.core;

import java.net.InetSocketAddress;
import java.util.Objects;

public record BackendServer(String id, String address) {
    public BackendServer {
        if (id == null || id.isBlank()) {
            throw new IllegalArgumentException("server id is required");
        }
        if (address == null || address.isBlank()) {
            throw new IllegalArgumentException("server address is required");
        }
    }

    public InetSocketAddress socketAddress() {
        int separator = address.lastIndexOf(':');
        if (separator <= 0 || separator == address.length() - 1) {
            throw new IllegalArgumentException("invalid server address: " + address);
        }
        String host = address.substring(0, separator);
        if (host.startsWith("[") && host.endsWith("]")) {
            host = host.substring(1, host.length() - 1);
        }
        int port;
        try {
            port = Integer.parseInt(address.substring(separator + 1));
        } catch (NumberFormatException error) {
            throw new IllegalArgumentException("invalid server port: " + address, error);
        }
        if (port < 1 || port > 65535) {
            throw new IllegalArgumentException("invalid server port: " + address);
        }
        return new InetSocketAddress(Objects.requireNonNull(host), port);
    }
}
