import assert from "node:assert/strict";
import test from "node:test";

import { invertFileSelection, selectFileEntry } from "../src/fileSelection.js";

const entries = ["a", "b", "c", "d"].map((path) => ({ path }));

test("plain click selects only the clicked entry", () => {
  const result = selectFileEntry(entries, [entries[0]], entries[2]);
  assert.deepEqual(result.selected, [entries[2]]);
  assert.equal(result.lastIndex, 2);
});

test("ctrl click toggles one entry", () => {
  const added = selectFileEntry(entries, [entries[0]], entries[2], { ctrlKey: true }, 0);
  assert.deepEqual(added.selected, [entries[0], entries[2]]);
  const removed = selectFileEntry(entries, added.selected, entries[0], { ctrlKey: true }, 2);
  assert.deepEqual(removed.selected, [entries[2]]);
});

test("shift click selects a contiguous range", () => {
  const result = selectFileEntry(entries, [entries[0]], entries[3], { shiftKey: true }, 0);
  assert.deepEqual(result.selected, entries);
});

test("inverse selection returns all unselected entries", () => {
  assert.deepEqual(invertFileSelection(entries, [entries[1], entries[3]]), [entries[0], entries[2]]);
});
