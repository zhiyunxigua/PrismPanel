import assert from "node:assert/strict";
import test from "node:test";
import { consoleLineToAnsi, consoleLogLevel, minecraftToAnsi } from "../src/components/servers/minecraft-format.js";

test("converts Minecraft hex colors to ANSI true color", () => {
  assert.equal(
    minecraftToAnsi("\u00a7x\u00a71\u00a72\u00a73\u00a74\u00a75\u00a76Hello"),
    "\u001b[38;2;18;52;86mHello",
  );
});

test("converts legacy colors and styles without changing normal text", () => {
  assert.equal(
    minecraftToAnsi("\u00a7cRed \u00a7lBold\u00a7r normal \u00a7nUnder"),
    "\u001b[38;2;255;85;85mRed \u001b[1mBold\u001b[0m normal \u001b[4mUnder",
  );
});

test("leaves unsupported Minecraft sequences untouched", () => {
  assert.equal(minecraftToAnsi("plain \u00a7z text"), "plain \u00a7z text");
  assert.equal(minecraftToAnsi("plain \u001b[31mANSI"), "plain \u001b[31mANSI");
});

test("detects common Minecraft log levels", () => {
  assert.equal(consoleLogLevel("[20:06:12 INFO]: Done"), "INFO");
  assert.equal(consoleLogLevel("[20:06:12 WARN]: Slow"), "WARN");
  assert.equal(consoleLogLevel("[20:06:12 WARNING]: Slow"), "WARN");
  assert.equal(consoleLogLevel("[20:06:12 ERROR]: Failed"), "ERROR");
  assert.equal(consoleLogLevel("[20:06:12] [Server thread/INFO]: Done"), "INFO");
  assert.equal(consoleLogLevel("[20:06:12] [Server thread/SEVERE]: Failed"), "ERROR");
  assert.equal(consoleLogLevel("unstructured output"), "");
});

test("adds a base level color while preserving inline formatting", () => {
  assert.equal(
    consoleLineToAnsi("[20:06:12 WARN]: \u00a7cDanger"),
    "\u001b[38;2;255;216;102m[20:06:12 WARN]: \u001b[38;2;255;85;85mDanger",
  );
  assert.equal(
    consoleLineToAnsi("[20:06:12] [Server thread/INFO]: Done"),
    "\u001b[38;2;255;255;255m[20:06:12] [Server thread/INFO]: Done",
  );
});
