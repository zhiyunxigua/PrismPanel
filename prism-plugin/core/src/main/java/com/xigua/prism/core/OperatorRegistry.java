package com.xigua.prism.core;

import java.util.List;
import java.util.function.Consumer;

public interface OperatorRegistry extends AutoCloseable {
    OperatorApplyResult replace(long revision, boolean active, List<ManagedOperator> operators);

    void setDriftReporter(Consumer<OperatorDriftReport> reporter);

    @Override
    default void close() {
    }
}
