package com.xigua.prism.core;

public final class PrismCommandException extends RuntimeException {
    private final String code;

    public PrismCommandException(String code, String message) {
        super(message);
        this.code = code;
    }

    public String code() {
        return code;
    }
}
