package com.xigua.prism.core;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.HexFormat;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

public final class FileFingerprintCache {
    private final Map<Path, Entry> entries = new ConcurrentHashMap<>();

    public void add(Path source, Map<String, Object> target) {
        try {
            Path normalized = source.toAbsolutePath().normalize();
            Path fileName = normalized.getFileName();
            if (fileName != null) {
                target.put("source_file", fileName.toString());
            }
            if (!Files.isRegularFile(normalized)) {
                return;
            }
            long size = Files.size(normalized);
            long modified = Files.getLastModifiedTime(normalized).toMillis();
            Entry cached = entries.get(normalized);
            if (cached == null || cached.size() != size || cached.modified() != modified) {
                cached = new Entry(size, modified, sha256(normalized));
                entries.put(normalized, cached);
            }
            target.put("sha256", cached.sha256());
        } catch (IOException | RuntimeException ignored) {
        }
    }

    private String sha256(Path path) throws IOException {
        try {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            try (var input = Files.newInputStream(path)) {
                byte[] buffer = new byte[64 * 1024];
                int count;
                while ((count = input.read(buffer)) >= 0) {
                    if (count > 0) {
                        digest.update(buffer, 0, count);
                    }
                }
            }
            return HexFormat.of().formatHex(digest.digest());
        } catch (NoSuchAlgorithmException error) {
            throw new IllegalStateException("SHA-256 is unavailable", error);
        }
    }

    private record Entry(long size, long modified, String sha256) {
    }
}
