import assert from "node:assert/strict";
import test from "node:test";

import { formatBytes } from "../src/formatBytes.js";

globalThis.window = {
  __PRISM_RUNTIME__: {
    apiBaseUrl: "http://panel.test/",
  },
  location: { href: "http://panel.test/" },
  dispatchEvent: () => {},
  showSaveFilePicker: undefined,
};

// ---- 字节换算 ----

test("formatBytes converts byte counts to readable sizes", () => {
  assert.equal(formatBytes(0), "0 B");
  assert.equal(formatBytes(512), "512 B");
  assert.equal(formatBytes(1024), "1.0 KB");
  assert.equal(formatBytes(1536), "1.5 KB");
  assert.equal(formatBytes(1024 ** 2), "1.00 MB");
  assert.equal(formatBytes(12.3 * 1024 ** 2), "12.30 MB");
  assert.equal(formatBytes(1024 ** 3), "1.00 GB");
  assert.equal(formatBytes(undefined), "0 B");
  assert.equal(formatBytes(null), "0 B");
  assert.equal(formatBytes("2048"), "2.0 KB");
});

// ---- 上传进度（XMLHttpRequest 模拟）----

class FakeXHR {
  constructor() {
    this.upload = {};
    this.headers = {};
    this.sent = null;
    this.aborted = false;
    this.onabort = null;
  }
  open(method, url) {
    this.method = method;
    this.url = url;
  }
  setRequestHeader(key, value) {
    this.headers[key] = value;
  }
  send(body) {
    this.sent = body;
  }
  getResponseHeader() {
    return null;
  }
  abort() {
    this.aborted = true;
    this.onabort?.();
  }
  complete(status = 200, response = { success: true, data: { uploaded: true } }) {
    this.status = status;
    this.response = response;
    this.onload?.();
  }
  fail() {
    this.onerror?.();
  }
  fireUploadProgress(loaded, total) {
    this.upload.onprogress?.({ lengthComputable: true, loaded, total });
  }
  failUploadProgress(loaded) {
    this.upload.onprogress?.({ lengthComputable: false, loaded });
  }
}

const uploadProgressEvents = [];
let uploadXHR;

function installUploadXHRMock() {
  uploadXHR = null;
  globalThis.XMLHttpRequest = class extends FakeXHR {
    constructor() {
      super();
      uploadXHR = this;
    }
  };
}

async function waitForXHR() {
  for (let i = 0; i < 50 && !uploadXHR; i++) {
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
}

function grantResponse(path = "/data") {
  return {
    ok: true,
    status: 200,
    json: async () => ({
      success: true,
      data: {
        ticket: "ticket-1",
        mode: "direct",
        endpoint: "http://node.test/upload",
        resource_type: "instance",
        resource_id: "instance-1",
        path,
      },
    }),
  };
}

function proxyGrantResponse(path = "logs/测试.log") {
  return {
    ok: true,
    status: 200,
    json: async () => ({
      success: true,
      data: {
        ticket: "ticket-proxy",
        mode: "proxy",
        endpoint: "/api/v1/files/proxy/upload?node_id=node-1",
        resource_type: "instance",
        resource_id: "instance-1",
        path,
      },
    }),
  };
}

let lastFetchURL = "";
globalThis.fetch = async (url, options) => {
  lastFetchURL = String(url);
  if (String(url).includes("/api/v1/files/authorize")) {
    let input = {};
    try {
      input = JSON.parse(options?.body || "{}");
    } catch {
      input = {};
    }
    return grantResponse(input.path);
  }
  return { ok: true, status: 200, json: async () => ({ success: true, data: {} }) };
};

const fileApi = await import("../src/fileApi.js");

test("uploadFileWithProgress fires byte-level progress and resolves data", async () => {
  installUploadXHRMock();
  uploadProgressEvents.length = 0;

  const promise = fileApi.uploadFileWithProgress(
    { node_id: "node-1", scope: "file.upload", resource_type: "instance", resource_id: "instance-1", path: "/data" },
    { name: "a.bin", size: 100, type: "application/octet-stream" },
    false,
    (event) => uploadProgressEvents.push(event),
  );
  await waitForXHR();
  assert.ok(uploadXHR, "XHR should be created");
  uploadXHR.fireUploadProgress(25, 100);
  uploadXHR.fireUploadProgress(60, 100);
  uploadXHR.fireUploadProgress(100, 100);
  uploadXHR.complete();
  const result = await promise;

  assert.deepEqual(result, { uploaded: true });
  assert.deepEqual(uploadProgressEvents, [
    { loaded: 25, total: 100 },
    { loaded: 60, total: 100 },
    { loaded: 100, total: 100 },
  ]);
  assert.equal(uploadXHR.method, "POST");
  assert.equal(uploadXHR.url, "http://node.test/upload?path=%2Fdata");
  assert.equal(uploadXHR.headers["Authorization"], "Bearer ticket-1");
  assert.equal(uploadXHR.headers["X-Prism-Resource-Type"], "instance");
  assert.equal(uploadXHR.headers["X-Prism-Resource-ID"], "instance-1");
  assert.equal(uploadXHR.headers["X-Prism-Path"], undefined);
  assert.equal(uploadXHR.headers["X-Prism-Overwrite"], "false");
  assert.equal(uploadXHR.headers["Content-Type"], "application/octet-stream");
});

test("uploadFileWithProgress percent-encodes a Chinese path in the URL query", async () => {
  installUploadXHRMock();
  const promise = fileApi.uploadFileWithProgress(
    { node_id: "node-1", scope: "file.upload", resource_type: "instance", resource_id: "instance-1", path: "/数据/测试文件.txt" },
    { name: "测试文件.txt", size: 10, type: "application/octet-stream" },
    false,
    (event) => {},
  );
  await waitForXHR();
  assert.ok(uploadXHR, "XHR should be created");
  uploadXHR.complete();
  await promise;

  assert.equal(uploadXHR.url, "http://node.test/upload?path=%2F%E6%95%B0%E6%8D%AE%2F%E6%B5%8B%E8%AF%95%E6%96%87%E4%BB%B6.txt");
  // 中文路径不得再放入任何 header（Headers 构造只允许 ISO-8859-1 字节）
  assert.equal(uploadXHR.headers["X-Prism-Path"], undefined);
});

test("uploadFileWithProgress appends path to proxy endpoint without dropping existing query", async () => {
  installUploadXHRMock();
  const savedFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    if (String(url).includes("/api/v1/files/authorize")) return proxyGrantResponse();
    return { ok: true, status: 200, json: async () => ({ success: true, data: {} }) };
  };
  try {
    const promise = fileApi.uploadFileWithProgress(
      { node_id: "node-1", scope: "file.upload", resource_type: "instance", resource_id: "instance-1", path: "logs/测试.log" },
      { name: "测试.log", size: 10, type: "application/octet-stream" },
    );
    await waitForXHR();
    assert.ok(uploadXHR, "XHR should be created");
    uploadXHR.complete();
    await promise;
  } finally {
    globalThis.fetch = savedFetch;
  }

  const url = new URL(uploadXHR.url);
  assert.equal(url.searchParams.get("path"), "logs/测试.log");
  assert.equal(url.searchParams.get("node_id"), "node-1");
  assert.equal(uploadXHR.headers["X-Prism-Path"], undefined);
});

test("uploadFileWithProgress ignores non-computable progress events", async () => {
  installUploadXHRMock();
  uploadProgressEvents.length = 0;

  const promise = fileApi.uploadFileWithProgress(
    { node_id: "node-1", scope: "file.upload", resource_type: "instance", resource_id: "instance-1", path: "/data" },
    { name: "a.bin", size: 100, type: "application/octet-stream" },
    false,
    (event) => uploadProgressEvents.push(event),
  );
  await waitForXHR();
  assert.ok(uploadXHR, "XHR should be created");
  uploadXHR.failUploadProgress(10);
  uploadXHR.fireUploadProgress(50, 100);
  uploadXHR.complete();
  const result = await promise;

  assert.deepEqual(result, { uploaded: true });
  assert.deepEqual(uploadProgressEvents, [{ loaded: 50, total: 100 }]);
});

test("legacy uploadFile still resolves without a progress callback", async () => {
  installUploadXHRMock();
  const promise = fileApi.uploadFile(
    { node_id: "node-1", scope: "file.upload", resource_type: "instance", resource_id: "instance-1", path: "/data" },
    { name: "a.bin", size: 10, type: "application/octet-stream" },
  );
  await waitForXHR();
  assert.ok(uploadXHR, "XHR should be created");
  uploadXHR.complete();
  const result = await promise;
  assert.deepEqual(result, { uploaded: true });
});

// ---- 上传取消 ----

test("uploadFileWithProgress exposes a cancel() handle on the returned promise", async () => {
  installUploadXHRMock();
  const handle = fileApi.uploadFileWithProgress(
    { node_id: "node-1", scope: "file.upload", resource_type: "instance", resource_id: "instance-1", path: "/data" },
    { name: "a.bin", size: 100, type: "application/octet-stream" },
  );
  await waitForXHR();
  assert.ok(uploadXHR, "XHR should be created");
  assert.equal(typeof handle.cancel, "function");
  assert.equal(uploadXHR.aborted, false);
  const rejected = assert.rejects(handle, (error) => error.name === "AbortError");
  handle.cancel();
  assert.equal(uploadXHR.aborted, true);
  await rejected;
});

test("cancelling the upload rejects with AbortError and stops progress", async () => {
  installUploadXHRMock();
  const events = [];
  const handle = fileApi.uploadFileWithProgress(
    { node_id: "node-1", scope: "file.upload", resource_type: "instance", resource_id: "instance-1", path: "/data" },
    { name: "a.bin", size: 100, type: "application/octet-stream" },
    false,
    (event) => events.push(event),
  );
  await waitForXHR();
  assert.ok(uploadXHR, "XHR should be created");
  const rejected = assert.rejects(handle, (error) => error.name === "AbortError");
  uploadXHR.fireUploadProgress(30, 100);
  handle.cancel();
  await rejected;
  assert.equal(uploadXHR.aborted, true);
  assert.deepEqual(events, [{ loaded: 30, total: 100 }]);
});

test("uploadFileWithProgress honors an external AbortSignal", async () => {
  installUploadXHRMock();
  const controller = new AbortController();
  const handle = fileApi.uploadFileWithProgress(
    { node_id: "node-1", scope: "file.upload", resource_type: "instance", resource_id: "instance-1", path: "/data" },
    { name: "a.bin", size: 100, type: "application/octet-stream" },
    false,
    undefined,
    controller.signal,
  );
  await waitForXHR();
  assert.ok(uploadXHR, "XHR should be created");
  assert.equal(uploadXHR.aborted, false);
  controller.abort();
  assert.equal(uploadXHR.aborted, true);
  await assert.rejects(handle, (error) => error.name === "AbortError");
});

test("pre-aborted signal never starts the upload", async () => {
  installUploadXHRMock();
  const controller = new AbortController();
  controller.abort();
  const handle = fileApi.uploadFileWithProgress(
    { node_id: "node-1", scope: "file.upload", resource_type: "instance", resource_id: "instance-1", path: "/data" },
    { name: "a.bin", size: 100, type: "application/octet-stream" },
    false,
    undefined,
    controller.signal,
  );
  await assert.rejects(handle, (error) => error.name === "AbortError");
  assert.equal(uploadXHR, null, "XHR should never be created for a pre-aborted signal");
});

// ---- 下载进度（response.body 流式 + Content-Length）----

const writtenChunks = [];
const downloadProgressEvents = [];
let savePickerWritable = null;

globalThis.window.showSaveFilePicker = async () => ({
  createWritable: async () => savePickerWritable,
});

function makeDownloadResponse(chunkSizes, contentLength) {
  const stream = new ReadableStream({
    start(controller) {
      for (const size of chunkSizes) controller.enqueue(new Uint8Array(size));
      controller.close();
    },
  });
  return {
    ok: true,
    status: 200,
    headers: { get: (name) => (name === "Content-Length" ? String(contentLength) : null) },
    body: stream,
  };
}

function makeWritable() {
  writtenChunks.length = 0;
  savePickerWritable = new WritableStream({
    write(chunk) { writtenChunks.push(chunk.byteLength); },
    close() {},
    abort() {},
  });
  return savePickerWritable;
}

test("downloadFile streams chunks and reports byte progress", async () => {
  downloadProgressEvents.length = 0;
  makeWritable();
  let downloads = 0;
  const savedFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    if (String(url).includes("/api/v1/files/authorize")) return grantResponse();
    downloads += 1;
    return makeDownloadResponse([5, 5], 10);
  };

  try {
    await fileApi.downloadFile(
      { node_id: "node-1", scope: "file.download", resource_type: "instance", resource_id: "instance-1", path: "/data/a.bin" },
      "a.bin",
      (event) => downloadProgressEvents.push(event),
    );
  } finally {
    globalThis.fetch = savedFetch;
  }

  assert.equal(downloads, 1);
  assert.deepEqual(writtenChunks, [5, 5]);
  assert.equal(downloadProgressEvents.length, 2);
  assert.deepEqual(downloadProgressEvents[0], { loaded: 5, total: 10, percent: 50, rate: downloadProgressEvents[0].rate });
  assert.equal(downloadProgressEvents[0].percent, 50);
  assert.equal(downloadProgressEvents[1].loaded, 10);
  assert.equal(downloadProgressEvents[1].total, 10);
  assert.equal(downloadProgressEvents[1].percent, 100);
  assert.ok(downloadProgressEvents[1].rate > 0);
});

test("downloadFile without onProgress keeps the pipeTo path", async () => {
  makeWritable();
  let downloads = 0;
  const savedFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    if (String(url).includes("/api/v1/files/authorize")) return grantResponse();
    downloads += 1;
    return makeDownloadResponse([7, 3], 10);
  };

  try {
    await fileApi.downloadFile(
      { node_id: "node-1", scope: "file.download", resource_type: "instance", resource_id: "instance-1", path: "/data/a.bin" },
      "a.bin",
    );
  } finally {
    globalThis.fetch = savedFetch;
  }

  assert.equal(downloads, 1);
  assert.deepEqual(writtenChunks, [7, 3]);
});

test("downloadFile reports bytes when Content-Length is unknown", async () => {
  downloadProgressEvents.length = 0;
  makeWritable();
  const savedFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    if (String(url).includes("/api/v1/files/authorize")) return grantResponse();
    return makeDownloadResponse([4, 4, 2], 0);
  };

  try {
    await fileApi.downloadFile(
      { node_id: "node-1", scope: "file.download", resource_type: "instance", resource_id: "instance-1", path: "/data/a.bin" },
      "a.bin",
      (event) => downloadProgressEvents.push(event),
    );
  } finally {
    globalThis.fetch = savedFetch;
  }

  assert.deepEqual(downloadProgressEvents.map((event) => event.loaded), [4, 8, 10]);
  assert.ok(downloadProgressEvents.every((event) => event.total === 0 && event.percent === 0));
});

// ---- requestWithProgress（面板 API 上传，如插件/仓库上传）----

const apiModule = await import("../src/api.js");

test("requestWithProgress reports upload progress and decodes data", async () => {
  installUploadXHRMock();
  uploadProgressEvents.length = 0;
  globalThis.window.dispatchEvent = () => {};

  const promise = apiModule.requestWithProgress(
    "/api/v1/plugins",
    { method: "POST", body: new FormData() },
    (event) => uploadProgressEvents.push(event),
  );
  await waitForXHR();
  assert.ok(uploadXHR, "XHR should be created");
  uploadXHR.fireUploadProgress(128, 1024);
  uploadXHR.fireUploadProgress(1024, 1024);
  uploadXHR.complete();
  const result = await promise;

  assert.deepEqual(result, { uploaded: true });
  assert.deepEqual(uploadProgressEvents, [
    { loaded: 128, total: 1024 },
    { loaded: 1024, total: 1024 },
  ]);
  assert.equal(uploadXHR.url, "http://panel.test/api/v1/plugins");
  assert.equal(uploadXHR.method, "POST");
});

test("requestWithProgress surfaces ApiError payloads", async () => {
  installUploadXHRMock();
  const promise = apiModule.requestWithProgress("/api/v1/plugins", { method: "POST", body: new FormData() });
  await waitForXHR();
  assert.ok(uploadXHR, "XHR should be created");
  uploadXHR.complete(409, {
    success: false,
    error: { code: "PLUGIN_EXISTS", message: "插件已存在" },
    data: { existing_version: "1.0" },
  });
  await assert.rejects(promise, (error) => {
    assert.equal(error.code, "PLUGIN_EXISTS");
    assert.equal(error.message, "插件已存在");
    assert.deepEqual(error.data, { existing_version: "1.0" });
    return true;
  });
});
