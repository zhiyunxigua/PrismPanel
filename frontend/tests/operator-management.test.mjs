import test from "node:test";
import assert from "node:assert/strict";
import {
  mergeOnlinePlayers,
  normalizeMinecraftUUID,
  operatorNodeState,
} from "../src/components/operators/operator-management.js";

test("normalizes Minecraft UUIDs", () => {
  assert.equal(
    normalizeMinecraftUUID("123E4567E89B12D3A456426614174000"),
    "123e4567-e89b-12d3-a456-426614174000",
  );
  assert.equal(normalizeMinecraftUUID("invalid"), "");
});

test("deduplicates online players across instances", () => {
  const players = mergeOnlinePlayers([{ instances: [
    { instance_id: "lobby", players: [{ uuid: "123e4567-e89b-12d3-a456-426614174000", name: "Steve" }] },
    { instance_id: "survival", players: [{ uuid: "123e4567e89b12d3a456426614174000", name: "Steve" }] },
  ] }]);
  assert.equal(players.length, 1);
  assert.deepEqual(players[0].locations, ["lobby", "survival"]);
});

test("derives node status from target failures", () => {
  assert.equal(operatorNodeState({ state: "pending" }), "pending");
  assert.equal(operatorNodeState({ state: "synced", result: { targets: [{ state: "failed" }] } }), "failed");
  assert.equal(operatorNodeState({ state: "synced", result: { targets: [{ state: "synced" }] } }), "synced");
});
