package com.xigua.prism.core;

import java.util.Map;

@FunctionalInterface
public interface TelemetryProvider {
    Map<String, Object> snapshot();
}
