package com.xigua.prism.core;

import java.util.List;

public record BackendCatalog(long revision, List<BackendServer> servers) {
    public BackendCatalog {
        if (revision < 0) {
            throw new IllegalArgumentException("revision must not be negative");
        }
        // daemon 在无后端配置时可能发送 servers:null，防御性视为空列表，避免 List.copyOf(null) NPE。
        servers = servers == null ? List.of() : List.copyOf(servers);
    }
}
