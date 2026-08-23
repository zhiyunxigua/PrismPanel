package com.xigua.prism.fabric;

import com.xigua.prism.core.PrismCore;
import net.fabricmc.api.ModInitializer;

/**
 * PrismPanel Fabric 运行态上报入口。
 *
 * <p>通过 Fabric Loader 的 ModInitializer 入口在服务端启动时执行：读取 daemon 注入的
 * PRISM_* 环境变量，可用则连接 daemon 的 /api/v1/ws/plugin 上报已加载 mod 列表；
 * 环境变量缺失（例如客户端误装）时静默禁用，不影响游戏运行。
 */
public final class PrismFabric implements ModInitializer {
    private PrismCore core;

    @Override
    public void onInitialize() {
        FabricLogger logger = new FabricLogger();
        core = PrismCore.create(
                "fabric",
                logger,
                new FabricScheduler(),
                new FabricTelemetry(),
                null,
                null,
                null
        ).orElse(null);
        if (core == null) {
            logger.info("Prism daemon environment is unavailable; integration is disabled.");
            return;
        }
        Runtime.getRuntime().addShutdownHook(new Thread(this::close, "prism-fabric-shutdown"));
        core.start();
        logger.info("Prism Fabric integration enabled; reporting loaded mods to daemon.");
    }

    private void close() {
        if (core != null) {
            core.close();
            core = null;
        }
    }
}
