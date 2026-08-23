import assert from "node:assert/strict";
import test from "node:test";

import { isExternalFileDrag, plainUploadItems, scanDroppedItems } from "../src/fileDrop.js";

test("scans File System Access handles including empty directories", async () => {
  const alpha = { name: "alpha.jar", size: 1 };
  const beta = { name: "beta.yml", size: 2 };
  const root = directoryHandle("pack", [
    directoryHandle("empty", []),
    fileHandle("alpha.jar", alpha),
    directoryHandle("nested", [fileHandle("beta.yml", beta)]),
  ]);

  const result = await scanDroppedItems({
    items: [{ kind: "file", getAsFileSystemHandle: async () => root }],
  });

  assert.deepEqual(result.directories, ["pack", "pack/empty", "pack/nested"]);
  assert.deepEqual(result.files.map((item) => item.path), ["pack/alpha.jar", "pack/nested/beta.yml"]);
  assert.equal(result.files[0].file, alpha);
  assert.equal(result.files[1].file, beta);
});

test("falls back to WebKit entries and reads every directory batch", async () => {
  const first = { name: "first.txt" };
  const second = { name: "second.txt" };
  const root = directoryEntry("folder", [
    [fileEntry("first.txt", first)],
    [directoryEntry("nested", [[fileEntry("second.txt", second)]])],
    [],
  ]);

  const result = await scanDroppedItems({
    items: [{
      kind: "file",
      getAsFileSystemHandle: async () => { throw new Error("unsupported"); },
      webkitGetAsEntry: () => root,
    }],
  });

  assert.deepEqual(result.directories, ["folder", "folder/nested"]);
  assert.deepEqual(result.files.map((item) => item.path), ["folder/first.txt", "folder/nested/second.txt"]);
});

test("normalizes relative paths and rejects traversal", () => {
  const result = plainUploadItems([{ name: "config.yml", webkitRelativePath: "pack\\config.yml" }]);
  assert.deepEqual(result.directories, ["pack"]);
  assert.deepEqual(result.files.map((item) => item.path), ["pack/config.yml"]);
  assert.throws(
    () => plainUploadItems([{ name: "secret", webkitRelativePath: "../secret" }]),
    /无效路径/,
  );
});

test("recognizes external file drags", () => {
  assert.equal(isExternalFileDrag({ dataTransfer: { types: ["text/plain", "Files"] } }), true);
  assert.equal(isExternalFileDrag({ dataTransfer: { types: ["text/plain"] } }), false);
});

function fileHandle(name, file) {
  return { kind: "file", name, getFile: async () => file };
}

function directoryHandle(name, children) {
  return {
    kind: "directory",
    name,
    async *values() {
      for (const child of children) yield child;
    },
  };
}

function fileEntry(name, file) {
  return { isFile: true, isDirectory: false, name, file: (resolve) => resolve(file) };
}

function directoryEntry(name, batches) {
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader() {
      let index = 0;
      return { readEntries: (resolve) => resolve(batches[index++] || []) };
    },
  };
}
