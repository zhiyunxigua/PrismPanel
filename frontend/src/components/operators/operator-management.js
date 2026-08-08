export function normalizeMinecraftUUID(value) {
  const compact = String(value || "").trim().toLowerCase().replaceAll("-", "");
  if (!/^[0-9a-f]{32}$/.test(compact)) return "";
  return [
    compact.slice(0, 8), compact.slice(8, 12), compact.slice(12, 16),
    compact.slice(16, 20), compact.slice(20),
  ].join("-");
}

export function mergeOnlinePlayers(nodeContents) {
  const players = new Map();
  for (const content of nodeContents || []) {
    for (const instance of content.instances || []) {
      for (const player of instance.players || []) {
        const uuid = normalizeMinecraftUUID(player.uuid);
        if (!uuid) continue;
        const current = players.get(uuid) || {
          uuid,
          name: player.name || player.username || "",
          locations: [],
        };
        const location = instance.name || instance.instance_id;
        if (location && !current.locations.includes(location)) current.locations.push(location);
        if (!current.name) current.name = player.name || player.username || "";
        players.set(uuid, current);
      }
    }
  }
  return [...players.values()].sort((left, right) => (
    (left.name || left.uuid).localeCompare(right.name || right.uuid)
  ));
}

export function operatorNodeState(node) {
  if (!node || node.state !== "synced") return node?.state || "pending";
  const targets = Array.isArray(node.result?.targets) ? node.result.targets : [];
  if (targets.some((target) => target.state === "failed")) return "failed";
  if (targets.some((target) => target.state !== "synced")) return "pending";
  return "synced";
}
