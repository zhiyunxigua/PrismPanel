import assert from "node:assert/strict";
import test from "node:test";
import {
  COMMAND_HISTORY_LIMIT,
  addCommandHistory,
  navigateCommandHistory,
} from "../src/components/servers/console-history.js";

test("command history keeps unique recent commands", () => {
  let history = [];
  for (let index = 0; index <= COMMAND_HISTORY_LIMIT; index += 1) {
    history = addCommandHistory(history, "command-" + index);
  }
  history = addCommandHistory(history, "command-5");
  assert.equal(history.length, COMMAND_HISTORY_LIMIT);
  assert.equal(history[0], "command-5");
  assert.equal(history.filter((item) => item === "command-5").length, 1);
});

test("arrow navigation moves through history and restores the draft", () => {
  const history = ["third", "second", "first"];
  let navigation = { index: -1, draft: "" };
  let result = navigateCommandHistory(history, navigation, -1, "draft");
  assert.equal(result.value, "third");
  result = navigateCommandHistory(history, result.navigation, -1, result.value);
  assert.equal(result.value, "second");
  result = navigateCommandHistory(history, result.navigation, 1, result.value);
  assert.equal(result.value, "third");
  result = navigateCommandHistory(history, result.navigation, 1, result.value);
  assert.equal(result.value, "draft");
  assert.equal(result.navigation.index, -1);
});
