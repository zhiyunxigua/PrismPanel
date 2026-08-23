package com.xigua.prism.core;

import java.util.List;

public record OperatorDriftReport(long revision, List<String> restored, List<String> removed) {
}
