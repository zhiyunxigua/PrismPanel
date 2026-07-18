package com.xigua.prism.core;

public interface PrismLogger {
    void info(String message);

    void warn(String message);

    void error(String message, Throwable error);
}
