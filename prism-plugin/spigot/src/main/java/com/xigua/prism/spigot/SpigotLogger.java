package com.xigua.prism.spigot;

import com.xigua.prism.core.PrismLogger;

import java.util.logging.Level;
import java.util.logging.Logger;

final class SpigotLogger implements PrismLogger {
    private final Logger logger;

    SpigotLogger(Logger logger) {
        this.logger = logger;
    }

    @Override
    public void info(String message) {
        logger.info(message);
    }

    @Override
    public void warn(String message) {
        logger.warning(message);
    }

    @Override
    public void error(String message, Throwable error) {
        logger.log(Level.SEVERE, message, error);
    }
}
