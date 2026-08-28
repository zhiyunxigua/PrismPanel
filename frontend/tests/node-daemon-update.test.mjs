import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const source = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src/views/NodeDetailView.vue"), "utf8");

test("node detail no longer exposes daemon self-update", () => {
  assert.doesNotMatch(source, /更新守护进程/);
  assert.doesNotMatch(source, /\/api\/v1\/nodes\/" \+ route\.params\.id \+ "\/update/);
  assert.doesNotMatch(source, /X-Prism-SHA256/);
  assert.doesNotMatch(source, /updateOpen/);
  assert.doesNotMatch(source, /uploadDaemonBinary/);
  assert.doesNotMatch(source, /sha256File/);
});
