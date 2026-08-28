import assert from "node:assert/strict";
import test from "node:test";
import { sha256Bytes, sha256File } from "../src/sha256.js";

const ABC_DIGEST = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad";

function fileLike(text) {
  const bytes = new TextEncoder().encode(text);
  return {
    arrayBuffer() {
      return Promise.resolve(bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength));
    },
  };
}

test("sha256Bytes hashes the abc fixture", () => {
  assert.equal(sha256Bytes(new TextEncoder().encode("abc")), ABC_DIGEST);
});

test("sha256File falls back when WebCrypto subtle is missing", async () => {
  const originalCrypto = globalThis.crypto;
  Object.defineProperty(globalThis, "crypto", { configurable: true, value: undefined });
  try {
    assert.equal(await sha256File(fileLike("abc")), ABC_DIGEST);
  } finally {
    Object.defineProperty(globalThis, "crypto", { configurable: true, value: originalCrypto });
  }
});

test("sha256File falls back when subtle.digest is missing", async () => {
  const originalCrypto = globalThis.crypto;
  Object.defineProperty(globalThis, "crypto", { configurable: true, value: {} });
  try {
    assert.equal(await sha256File(fileLike("abc")), ABC_DIGEST);
  } finally {
    Object.defineProperty(globalThis, "crypto", { configurable: true, value: originalCrypto });
  }
});
