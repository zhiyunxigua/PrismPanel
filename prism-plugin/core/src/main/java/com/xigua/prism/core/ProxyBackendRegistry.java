package com.xigua.prism.core;

@FunctionalInterface
public interface ProxyBackendRegistry {
    BackendApplyResult replace(BackendCatalog catalog);
}
