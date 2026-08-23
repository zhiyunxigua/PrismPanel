package com.xigua.prism.core;

import java.time.Duration;
import java.util.Optional;
import java.util.concurrent.atomic.AtomicBoolean;

public final class PrismCore implements AutoCloseable {
    private final PrismLogger logger;
    private final PlatformScheduler scheduler;
    private final TelemetryProvider telemetry;
    private final DaemonBridge bridge;
    private final OperatorRegistry operatorRegistry;
    private final AtomicBoolean started = new AtomicBoolean(false);
    private AutoCloseable telemetryTask;

    private PrismCore(
            PrismEnvironment environment,
            PrismLogger logger,
            PlatformScheduler scheduler,
            TelemetryProvider telemetry,
            ProxyBackendRegistry backendRegistry,
            PlayerTransferService transferService,
            OperatorRegistry operatorRegistry
    ) {
        this.logger = logger;
        this.scheduler = scheduler;
        this.telemetry = telemetry;
        this.operatorRegistry = operatorRegistry;
        this.bridge = new DaemonBridge(
                environment, logger, scheduler, backendRegistry, transferService, operatorRegistry
        );
    }

    public static Optional<PrismCore> create(
            String platform,
            PrismLogger logger,
            PlatformScheduler scheduler,
            TelemetryProvider telemetry,
            ProxyBackendRegistry backendRegistry,
            PlayerTransferService transferService
    ) {
        return create(platform, logger, scheduler, telemetry, backendRegistry, transferService, null);
    }

    public static Optional<PrismCore> create(
            String platform,
            PrismLogger logger,
            PlatformScheduler scheduler,
            TelemetryProvider telemetry,
            ProxyBackendRegistry backendRegistry,
            PlayerTransferService transferService,
            OperatorRegistry operatorRegistry
    ) {
        return PrismEnvironment.fromSystem(logger, platform)
                .map(environment -> new PrismCore(
                        environment, logger, scheduler, telemetry, backendRegistry, transferService,
                        operatorRegistry
                ));
    }

    public void start() {
        if (!started.compareAndSet(false, true)) {
            return;
        }
        bridge.start();
        telemetryTask = scheduler.repeat(Duration.ofSeconds(5), Duration.ofSeconds(5), this::publishSnapshot);
    }

    private void publishSnapshot() {
        try {
            bridge.publishSnapshot(telemetry.snapshot());
        } catch (RuntimeException error) {
            logger.error("Failed to collect Prism telemetry.", error);
        }
    }

    @Override
    public void close() {
        if (!started.compareAndSet(true, false)) {
            return;
        }
        if (telemetryTask != null) {
            try {
                telemetryTask.close();
            } catch (Exception error) {
                logger.error("Failed to stop Prism telemetry task.", error);
            }
            telemetryTask = null;
        }
        bridge.close();
        if (operatorRegistry != null) {
            try {
                operatorRegistry.close();
            } catch (Exception error) {
                logger.error("Failed to stop operator management.", error);
            }
        }
    }
}
