package com.xigua.prism.core;

public record BackendApplyResult(long revision, int applied, int removed) {
}
