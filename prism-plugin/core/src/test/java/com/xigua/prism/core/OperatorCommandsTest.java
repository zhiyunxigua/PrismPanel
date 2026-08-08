package com.xigua.prism.core;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class OperatorCommandsTest {
    @Test
    void recognizesOperatorCommands() {
        assertTrue(OperatorCommands.isRestricted("op Steve"));
        assertTrue(OperatorCommands.isRestricted("/deop Steve"));
        assertTrue(OperatorCommands.isRestricted("minecraft:op Steve"));
        assertTrue(OperatorCommands.isRestricted(" /minecraft:deop Steve "));
        assertTrue(OperatorCommands.isRestricted("op\tSteve"));
    }

    @Test
    void allowsUnrelatedCommands() {
        assertFalse(OperatorCommands.isRestricted("say op Steve"));
        assertFalse(OperatorCommands.isRestricted("list"));
        assertFalse(OperatorCommands.isRestricted(""));
        assertFalse(OperatorCommands.isRestricted(null));
    }
}
