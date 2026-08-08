package com.xigua.prism.core;

import com.google.gson.Gson;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import com.google.gson.annotations.SerializedName;

import java.net.http.HttpClient;
import java.net.http.WebSocket;
import java.time.Duration;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;
import java.util.concurrent.CompletionStage;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

final class DaemonBridge implements AutoCloseable {
    private static final int MAX_QUEUED_MESSAGES = 256;
    private static final Duration COMMAND_TIMEOUT = Duration.ofSeconds(15);

    private final PrismEnvironment environment;
    private final PrismLogger logger;
    private final PlatformScheduler platformScheduler;
    private final ProxyBackendRegistry backendRegistry;
    private final PlayerTransferService transferService;
    private final OperatorRegistry operatorRegistry;
    private final List<String> capabilities;
    private final long pid = ProcessHandle.current().pid();
    private final Gson gson = new Gson();
    private final HttpClient client = HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(5))
            .build();
    private final ScheduledExecutorService scheduler = Executors.newSingleThreadScheduledExecutor(runnable -> {
        Thread thread = new Thread(runnable, "prism-daemon-bridge");
        thread.setDaemon(true);
        return thread;
    });
    private final AtomicReference<WebSocket> socket = new AtomicReference<>();
    private final AtomicBoolean authenticated = new AtomicBoolean(false);
    private final AtomicBoolean stopping = new AtomicBoolean(false);
    private final AtomicBoolean reconnectScheduled = new AtomicBoolean(false);
    private final AtomicInteger retryDelaySeconds = new AtomicInteger(2);
    private final ArrayDeque<OutboundMessage> sendQueue = new ArrayDeque<>();
    private boolean sending;

    DaemonBridge(
            PrismEnvironment environment,
            PrismLogger logger,
            PlatformScheduler platformScheduler,
            ProxyBackendRegistry backendRegistry,
            PlayerTransferService transferService,
            OperatorRegistry operatorRegistry
    ) {
        this.environment = environment;
        this.logger = logger;
        this.platformScheduler = platformScheduler;
        this.backendRegistry = backendRegistry;
        this.transferService = transferService;
        this.operatorRegistry = operatorRegistry;
        List<String> declared = new ArrayList<>();
        declared.add("telemetry");
        declared.add("plugin.inventory");
        if (backendRegistry != null) {
            declared.add("proxy.backends");
        }
        if (transferService != null) {
            declared.add("player.transfer");
        }
        if (operatorRegistry != null) {
            declared.add("operators.sync");
            operatorRegistry.setDriftReporter(this::publishOperatorDrift);
        }
        this.capabilities = List.copyOf(declared);
    }

    void start() {
        scheduler.scheduleAtFixedRate(this::sendHeartbeat, 10, 10, TimeUnit.SECONDS);
        connect();
    }

    void publishSnapshot(Map<String, Object> snapshot) {
        if (authenticated.get()) {
            send(Map.of("type", "snapshot", "data", snapshot), false);
        }
    }

    private void connect() {
        if (stopping.get()) {
            return;
        }
        client.newWebSocketBuilder()
                .connectTimeout(Duration.ofSeconds(5))
                .buildAsync(environment.endpoint(), new Listener())
                .whenComplete((ignored, error) -> {
                    if (error != null) {
                        scheduleReconnect();
                    }
                });
    }

    private void sendAuthentication() {
        Map<String, Object> message = new LinkedHashMap<>();
        message.put("type", "auth");
        message.put("token", environment.token());
        message.put("instance_id", environment.instanceId());
        message.put("session_id", environment.sessionId());
        message.put("pid", pid);
        message.put("platform", environment.platform());
        message.put("capabilities", capabilities);
        send(message, true);
    }

    private void sendHeartbeat() {
        if (authenticated.get()) {
            send(Map.of("type", "heartbeat"), false);
        }
    }

    private void send(Map<String, ?> message, boolean critical) {
        enqueue(new OutboundMessage(gson.toJson(message), critical));
    }

    private synchronized void enqueue(OutboundMessage message) {
        if (socket.get() == null) {
            return;
        }
        if (sendQueue.size() >= MAX_QUEUED_MESSAGES) {
            if (!message.critical()) {
                return;
            }
            OutboundMessage removable = sendQueue.stream()
                    .filter(item -> !item.critical())
                    .findFirst()
                    .orElse(null);
            if (removable == null) {
                logger.warn("Prism daemon send queue is full; reconnecting.");
                WebSocket current = socket.get();
                if (current != null) {
                    current.abort();
                    handleDisconnect(current);
                }
                return;
            }
            sendQueue.remove(removable);
        }
        sendQueue.addLast(message);
        drainQueue();
    }

    private synchronized void drainQueue() {
        WebSocket current = socket.get();
        OutboundMessage next = sendQueue.peekFirst();
        if (sending || current == null || next == null) {
            return;
        }
        sending = true;
        current.sendText(next.payload(), true).whenComplete((ignored, error) -> {
            synchronized (DaemonBridge.this) {
                sending = false;
                if (error == null && socket.get() == current) {
                    sendQueue.pollFirst();
                    drainQueue();
                }
            }
            if (error != null) {
                handleDisconnect(current);
            }
        });
    }

    private void handleMessage(WebSocket webSocket, String contents) {
        JsonObject message;
        try {
            message = JsonParser.parseString(contents).getAsJsonObject();
        } catch (RuntimeException error) {
            logger.warn("Prism daemon sent invalid JSON.");
            return;
        }
        String type = stringValue(message, "type");
        if ("auth.result".equals(type)) {
            handleAuthenticationResult(webSocket, message);
            return;
        }
        if ("error".equals(type)) {
            logger.warn("Prism daemon rejected a plugin message.");
            return;
        }
        if (!authenticated.get()) {
            return;
        }
        handleCommand(type, message);
    }

    private void handleAuthenticationResult(WebSocket webSocket, JsonObject message) {
        boolean success = message.has("success") && message.get("success").getAsBoolean();
        if (success) {
            authenticated.set(true);
            retryDelaySeconds.set(2);
            logger.info("Connected to Prism daemon for instance " + environment.instanceId() + ".");
            return;
        }
        logger.warn("Prism daemon rejected the instance credential.");
        webSocket.abort();
        handleDisconnect(webSocket);
    }

    private void handleCommand(String type, JsonObject message) {
        String requestId = stringValue(message, "request_id");
        if (requestId.isBlank()) {
            return;
        }
        JsonObject data = objectValue(message, "data");
        CompletableFuture<?> operation;
        try {
            operation = switch (type) {
                case "proxy.backends.replace" -> replaceBackends(data);
                case "player.transfer" -> transferPlayer(data);
                case "operators.replace" -> replaceOperators(data);
                default -> CompletableFuture.failedFuture(
                        new PrismCommandException("UNKNOWN_COMMAND", "unsupported command: " + type)
                );
            };
        } catch (RuntimeException error) {
            operation = CompletableFuture.failedFuture(error);
        }
        operation.orTimeout(COMMAND_TIMEOUT.toMillis(), TimeUnit.MILLISECONDS)
                .whenComplete((result, error) -> sendResponse(requestId, result, error));
    }

    private CompletableFuture<BackendApplyResult> replaceBackends(JsonObject data) {
        if (backendRegistry == null) {
            return CompletableFuture.failedFuture(new PrismCommandException(
                    "UNSUPPORTED_CAPABILITY", "proxy backend management is unavailable"
            ));
        }
        BackendCatalog catalog = gson.fromJson(data, BackendCatalog.class);
        if (catalog == null || catalog.servers() == null) {
            return CompletableFuture.failedFuture(
                    new PrismCommandException("INVALID_REQUEST", "backend catalog is required")
            );
        }
        return platformScheduler.call(() -> backendRegistry.replace(catalog));
    }

    private CompletableFuture<Map<String, Object>> transferPlayer(JsonObject data) {
        if (transferService == null) {
            return CompletableFuture.failedFuture(new PrismCommandException(
                    "UNSUPPORTED_CAPABILITY", "player transfer is unavailable"
            ));
        }
        TransferRequest request = gson.fromJson(data, TransferRequest.class);
        if (request == null || request.playerId() == null || request.targetServerId() == null
                || request.targetServerId().isBlank()) {
            return CompletableFuture.failedFuture(
                    new PrismCommandException("INVALID_REQUEST", "player_uuid and target_server_id are required")
            );
        }
        UUID playerId;
        try {
            playerId = UUID.fromString(request.playerId());
        } catch (IllegalArgumentException error) {
            return CompletableFuture.failedFuture(
                    new PrismCommandException("INVALID_REQUEST", "player_uuid is invalid")
            );
        }
        return platformScheduler.call(() -> transferService.transfer(playerId, request.targetServerId()))
                .thenCompose(operation -> operation)
                .thenApply(ignored -> Map.of(
                        "player_uuid", playerId.toString(),
                        "target_server_id", request.targetServerId()
                ));
    }

    private CompletableFuture<OperatorApplyResult> replaceOperators(JsonObject data) {
        if (operatorRegistry == null) {
            return CompletableFuture.failedFuture(new PrismCommandException(
                    "UNSUPPORTED_CAPABILITY", "operator management is unavailable"
            ));
        }
        OperatorCatalogRequest request = gson.fromJson(data, OperatorCatalogRequest.class);
        if (request == null || request.revision() < 0 || request.operators() == null) {
            return CompletableFuture.failedFuture(
                    new PrismCommandException("INVALID_REQUEST", "operator catalog is invalid")
            );
        }
        List<ManagedOperator> operators = new ArrayList<>(request.operators().size());
        java.util.HashSet<UUID> seen = new java.util.HashSet<>();
        for (OperatorRequest item : request.operators()) {
            if (item == null || item.uuid() == null) {
                return CompletableFuture.failedFuture(
                        new PrismCommandException("INVALID_REQUEST", "operator UUID is required")
                );
            }
            UUID uuid;
            try {
                uuid = UUID.fromString(item.uuid());
            } catch (IllegalArgumentException error) {
                return CompletableFuture.failedFuture(
                        new PrismCommandException("INVALID_REQUEST", "operator UUID is invalid")
                );
            }
            if (!seen.add(uuid)) {
                return CompletableFuture.failedFuture(
                        new PrismCommandException("INVALID_REQUEST", "operator UUID is duplicated")
                );
            }
            operators.add(new ManagedOperator(uuid, item.name() == null ? "" : item.name().trim()));
        }
        return platformScheduler.call(() -> operatorRegistry.replace(
                request.revision(), request.active(), List.copyOf(operators)
        ));
    }

    private void publishOperatorDrift(OperatorDriftReport report) {
        if (authenticated.get()) {
            send(Map.of("type", "operator.drift", "data", report), true);
        }
    }

    private void sendResponse(String requestId, Object result, Throwable error) {
        Map<String, Object> response = new LinkedHashMap<>();
        response.put("type", "response");
        response.put("request_id", requestId);
        Throwable cause = unwrap(error);
        if (cause == null) {
            response.put("success", true);
            if (result != null) {
                response.put("data", result);
            }
        } else {
            response.put("success", false);
            response.put("error", errorPayload(cause));
        }
        send(response, true);
    }

    private Map<String, String> errorPayload(Throwable error) {
        if (error instanceof PrismCommandException commandError) {
            return Map.of("code", commandError.code(), "message", commandError.getMessage());
        }
        if (error instanceof TimeoutException) {
            return Map.of("code", "COMMAND_TIMEOUT", "message", "platform command timed out");
        }
        logger.error("Prism platform command failed.", error);
        String message = error.getMessage() == null ? "platform command failed" : error.getMessage();
        return Map.of("code", "COMMAND_FAILED", "message", message);
    }

    private Throwable unwrap(Throwable error) {
        if (error == null) {
            return null;
        }
        Throwable current = error;
        while ((current instanceof CompletionException || current instanceof java.util.concurrent.ExecutionException)
                && current.getCause() != null) {
            current = current.getCause();
        }
        return current;
    }

    private void handleDisconnect(WebSocket webSocket) {
        if (!socket.compareAndSet(webSocket, null)) {
            return;
        }
        authenticated.set(false);
        synchronized (this) {
            sendQueue.clear();
            sending = false;
        }
        scheduleReconnect();
    }

    private void scheduleReconnect() {
        if (stopping.get() || !reconnectScheduled.compareAndSet(false, true)) {
            return;
        }
        int delay = retryDelaySeconds.getAndUpdate(current -> Math.min(current * 2, 30));
        scheduler.schedule(() -> {
            reconnectScheduled.set(false);
            connect();
        }, delay, TimeUnit.SECONDS);
    }

    @Override
    public void close() {
        stopping.set(true);
        authenticated.set(false);
        WebSocket current = socket.getAndSet(null);
        if (current != null) {
            current.abort();
        }
        synchronized (this) {
            sendQueue.clear();
            sending = false;
        }
        scheduler.shutdownNow();
    }

    private static String stringValue(JsonObject object, String name) {
        JsonElement value = object.get(name);
        return value == null || value.isJsonNull() ? "" : value.getAsString();
    }

    private static JsonObject objectValue(JsonObject object, String name) {
        JsonElement value = object.get(name);
        return value != null && value.isJsonObject() ? value.getAsJsonObject() : new JsonObject();
    }

    private record TransferRequest(
            @SerializedName("player_uuid") String playerId,
            @SerializedName("target_server_id") String targetServerId
    ) {
    }

    private record OperatorCatalogRequest(
            long revision,
            boolean active,
            List<OperatorRequest> operators
    ) {
    }

    private record OperatorRequest(String uuid, String name) {
    }

    private record OutboundMessage(String payload, boolean critical) {
    }

    private final class Listener implements WebSocket.Listener {
        private final StringBuilder text = new StringBuilder();

        @Override
        public void onOpen(WebSocket webSocket) {
            socket.set(webSocket);
            authenticated.set(false);
            synchronized (DaemonBridge.this) {
                sendQueue.clear();
                sending = false;
            }
            sendAuthentication();
            webSocket.request(1);
        }

        @Override
        public CompletionStage<?> onText(WebSocket webSocket, CharSequence data, boolean last) {
            text.append(data);
            if (last) {
                String contents = text.toString();
                text.setLength(0);
                handleMessage(webSocket, contents);
            }
            webSocket.request(1);
            return CompletableFuture.completedFuture(null);
        }

        @Override
        public CompletionStage<?> onClose(WebSocket webSocket, int statusCode, String reason) {
            handleDisconnect(webSocket);
            return CompletableFuture.completedFuture(null);
        }

        @Override
        public void onError(WebSocket webSocket, Throwable error) {
            handleDisconnect(webSocket);
        }
    }
}
