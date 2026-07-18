package com.xigua.prism.core;

import java.util.List;

public record BackendCatalog(long revision, List<BackendServer> servers) {
    public BackendCatalog {
        if (revision < 0) {
            throw new IllegalArgumentException("revision must not be negative");
        }
        servers = List.copyOf(servers);
    }
}
