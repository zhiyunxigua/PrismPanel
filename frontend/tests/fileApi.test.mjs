import assert from "node:assert/strict";
import test from "node:test";

globalThis.window = {
  __PRISM_RUNTIME__: { apiBaseUrl: "https://panel.example.test" },
  location: { href: "https://panel.example.test/servers/demo" },
  dispatchEvent() {},
};

const { fileJSON, uploadFile } = await import("../src/fileApi.js");

test("uploadFile sends stable sequential chunks and reports byte progress", async () => {
  const originalFetch = globalThis.fetch;
  const chunks = [];
  const progress = [];
  globalThis.fetch = async (url, options) => {
    if (String(url).includes("/api/v1/files/authorize")) {
      return jsonResponse({ success: true, data: {
        mode: "direct", endpoint: "https://node.example.test/api/v1/files/upload",
        ticket: "ticket-a", resource_type: "instance", resource_id: "demo", path: "large.bin",
        chunk_size: 4,
      } });
    }
    const body = new Uint8Array(await options.body.arrayBuffer());
    const offset = Number(options.headers["X-Prism-Upload-Offset"]);
    chunks.push({ offset, final: options.headers["X-Prism-Upload-Final"], body: Array.from(body), uploadId: options.headers["X-Prism-Upload-ID"] });
    return jsonResponse({ success: true, data: offset + body.length === 10
      ? { path: "large.bin", size: 10 }
      : { offset: offset + body.length, complete: false } });
  };
  try {
    const file = new Blob([Uint8Array.from({ length: 10 }, (_, index) => index)], { type: "application/octet-stream" });
    const result = await uploadFile(fileAuthorization(), file, false, {
      uploadId: "stable-upload-id",
      onProgress: ({ loaded }) => progress.push(loaded),
    });
    assert.equal(result.size, 10);
    assert.deepEqual(chunks.map((item) => item.offset), [0, 4, 8]);
    assert.deepEqual(chunks.map((item) => item.final), ["false", "false", "true"]);
    assert.ok(chunks.every((item) => item.uploadId === "stable-upload-id"));
    assert.deepEqual(progress.filter((value, index) => index === 0 || value !== progress[index - 1]), [0, 4, 8, 10]);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("fileJSON preserves structured node error details", async () => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = async () => {
    calls += 1;
    if (calls === 1) {
      return jsonResponse({ success: true, data: {
        mode: "direct", endpoint: "https://node.example.test/api/v1/files/create",
        ticket: "ticket-b", resource_type: "instance", resource_id: "demo", path: "folder",
      } });
    }
    return jsonResponse({ success: false, error: {
      code: "DISK_FULL", message: "磁盘空间不足", stage: "file-operation",
      details: ["no space left on device"], retryable: false,
    }, request_id: "request-42" }, 500);
  };
  try {
    await assert.rejects(
      fileJSON({ ...fileAuthorization(), scope: "file.create", path: "folder" }, "POST", { type: "directory" }),
      (error) => error.code === "DISK_FULL" && error.stage === "file-operation" &&
        error.requestId === "request-42" && error.details[0] === "no space left on device",
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
});

function fileAuthorization() {
  return { node_id: "node-a", scope: "file.upload", resource_type: "instance", resource_id: "demo", path: "large.bin" };
}

function jsonResponse(payload, status = 200) {
  return new Response(JSON.stringify(payload), { status, headers: { "Content-Type": "application/json" } });
}
