package com.xigua.prism.velocity;

import com.xigua.prism.core.PrismLogger;
import org.slf4j.Logger;

final class VelocityLogger implements PrismLogger {
    private final Logger logger;

    VelocityLogger(Logger logger) {
        this.logger = logger;
    }

    @Override
    public void info(String message) {
        logger.info(message);
    }

    @Override
    public void warn(String message) {
        logger.warn(message);
    }

    @Override
    public void error(String message, Throwable error) {
        logger.error(message, error);
    }
}
