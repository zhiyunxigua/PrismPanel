package com.xigua.prismMC.daemon;

import com.google.gson.Gson;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import org.bukkit.plugin.java.JavaPlugin;

import java.net.InetAddress;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.WebSocket;
import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionStage;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

public final class DaemonBridge implements AutoCloseable {
    private final JavaPlugin plugin;
    private final URI endpoint;
    private final String instanceId;
    private final String sessionId;
    private final String token;
    private final long pid;
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
    private CompletableFuture<WebSocket> pendingSend = CompletableFuture.completedFuture(null);

    private DaemonBridge(
            JavaPlugin plugin,
            URI endpoint,
            String instanceId,
            String sessionId,
            String token
    ) {
        this.plugin = plugin;
        this.endpoint = endpoint;
        this.instanceId = instanceId;
        this.sessionId = sessionId;
        this.token = token;
        this.pid = ProcessHandle.current().pid();
    }

    public static DaemonBridge fromEnvironment(JavaPlugin plugin) {
        String endpoint = System.getenv("PRISM_DAEMON_WS");
        String instanceId = System.getenv("PRISM_INSTANCE_ID");
        String sessionId = System.getenv("PRISM_SESSION_ID");
        String token = System.getenv("PRISM_PLUGIN_TOKEN");
        if (isBlank(endpoint) || isBlank(instanceId) || isBlank(sessionId) || isBlank(token)) {
            return null;
        }
        try {
            URI uri = URI.create(endpoint);
            if (!isLocalWebSocket(uri)) {
                plugin.getLogger().severe("Prism daemon endpoint must be a loopback WebSocket URL.");
                return null;
            }
            return new DaemonBridge(plugin, uri, instanceId, sessionId, token);
        } catch (RuntimeException exception) {
            plugin.getLogger().severe("Prism daemon endpoint is invalid: " + exception.getMessage());
            return null;
        }
    }

    public void start() {
        scheduler.scheduleAtFixedRate(this::sendHeartbeat, 10, 10, TimeUnit.SECONDS);
        connect();
    }

    public void publishSnapshot(Map<String, Object> snapshot) {
        if (!authenticated.get()) {
            return;
        }
        Map<String, Object> envelope = new LinkedHashMap<>();
        envelope.put("type", "snapshot");
        envelope.put("data", snapshot);
        send(envelope);
    }

    private void connect() {
        if (stopping.get()) {
            return;
        }
        client.newWebSocketBuilder()
                .connectTimeout(Duration.ofSeconds(5))
                .buildAsync(endpoint, new Listener())
                .whenComplete((webSocket, error) -> {
                    if (error != null) {
                        scheduleReconnect();
                    }
                });
    }

    private void sendAuthentication(WebSocket webSocket) {
        Map<String, Object> message = new LinkedHashMap<>();
        message.put("type", "auth");
        message.put("token", token);
        message.put("instance_id", instanceId);
        message.put("session_id", sessionId);
        message.put("pid", pid);
        send(webSocket, message);
    }

    private void sendHeartbeat() {
        if (!authenticated.get()) {
            return;
        }
        send(Map.of("type", "heartbeat"));
    }

    private void send(Map<String, Object> message) {
        WebSocket current = socket.get();
        if (current != null) {
            send(current, message);
        }
    }

    private synchronized void send(WebSocket webSocket, Map<String, Object> message) {
        String contents = gson.toJson(message);
        pendingSend = pendingSend
                .handle((ignored, previousError) -> webSocket)
                .thenCompose(ignored -> webSocket.sendText(contents, true));
        pendingSend.whenComplete((ignored, error) -> {
            if (error != null) {
                handleDisconnect(webSocket);
            }
        });
    }

    private void handleMessage(WebSocket webSocket, String contents) {
        JsonObject message;
        try {
            message = JsonParser.parseString(contents).getAsJsonObject();
        } catch (RuntimeException exception) {
            return;
        }
        String type = message.has("type") ? message.get("type").getAsString() : "";
        if ("auth.result".equals(type)) {
            boolean success = message.has("success") && message.get("success").getAsBoolean();
            if (success) {
                authenticated.set(true);
                retryDelaySeconds.set(2);
                plugin.getLogger().info("Connected to Prism daemon for instance " + instanceId + ".");
            } else {
                plugin.getLogger().warning("Prism daemon rejected the instance credential.");
                handleDisconnect(webSocket);
                webSocket.abort();
            }
        } else if ("error".equals(type) && message.has("error")) {
            plugin.getLogger().warning("Prism daemon rejected a telemetry message.");
        }
    }

    private void handleDisconnect(WebSocket webSocket) {
        if (!socket.compareAndSet(webSocket, null)) {
            return;
        }
        authenticated.set(false);
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
        scheduler.shutdownNow();
    }

    private static boolean isBlank(String value) {
        return value == null || value.isBlank();
    }

    private static boolean isLocalWebSocket(URI uri) {
        String scheme = uri.getScheme();
        if (!"ws".equalsIgnoreCase(scheme) && !"wss".equalsIgnoreCase(scheme)) {
            return false;
        }
        try {
            return uri.getHost() != null && InetAddress.getByName(uri.getHost()).isLoopbackAddress();
        } catch (Exception exception) {
            return false;
        }
    }

    private final class Listener implements WebSocket.Listener {
        private final StringBuilder text = new StringBuilder();

        @Override
        public void onOpen(WebSocket webSocket) {
            socket.set(webSocket);
            authenticated.set(false);
            synchronized (DaemonBridge.this) {
                pendingSend = CompletableFuture.completedFuture(webSocket);
            }
            sendAuthentication(webSocket);
            webSocket.request(1);
        }

        @Override
        public CompletionStage<?> onText(WebSocket webSocket, CharSequence data, boolean last) {
            text.append(data);
            if (last) {
                String message = text.toString();
                text.setLength(0);
                handleMessage(webSocket, message);
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
