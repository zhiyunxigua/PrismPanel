package com.xigua.prism.core;

import java.util.Locale;

public final class OperatorCommands {
    private OperatorCommands() {
    }

    public static boolean isRestricted(String command) {
        if (command == null) {
            return false;
        }
        String trimmed = command.trim();
        while (trimmed.startsWith("/")) {
            trimmed = trimmed.substring(1).trim();
        }
        if (trimmed.isEmpty()) {
            return false;
        }
        int separator = -1;
        for (int index = 0; index < trimmed.length(); index++) {
            if (Character.isWhitespace(trimmed.charAt(index))) {
                separator = index;
                break;
            }
        }
        String label = (separator < 0 ? trimmed : trimmed.substring(0, separator))
                .toLowerCase(Locale.ROOT);
        int namespace = label.lastIndexOf(':');
        if (namespace >= 0) {
            label = label.substring(namespace + 1);
        }
        return label.equals("op") || label.equals("deop");
    }
}
