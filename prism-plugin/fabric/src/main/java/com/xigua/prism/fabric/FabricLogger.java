package com.xigua.prism.fabric;

import com.xigua.prism.core.PrismLogger;

/**
 * Fabric 侧日志适配：输出到服务端标准输出（服务端控制台可见）。
 * Fabric Loader 未向 mod 暴露稳定的公共日志 API，避免额外依赖，使用 System.out。
 */
public final class FabricLogger implements PrismLogger {
    private static final String PREFIX = "[Prism] ";

    @Override
    public void info(String message) {
        System.out.println(PREFIX + message);
    }

    @Override
    public void warn(String message) {
        System.out.println(PREFIX + "[warn] " + message);
    }

    @Override
    public void error(String message, Throwable error) {
        System.out.println(PREFIX + "[error] " + message);
        if (error != null) {
            error.printStackTrace(System.out);
        }
    }
}
