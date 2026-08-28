export function selectFileEntry(entries, selected, entry, modifiers = {}, lastIndex = -1) {
  const index = entries.findIndex((item) => item.path === entry.path);
  if (index < 0) return { selected, lastIndex };
  if (modifiers.shiftKey && lastIndex >= 0) {
    const range = entries.slice(Math.min(lastIndex, index), Math.max(lastIndex, index) + 1);
    if (modifiers.ctrlKey || modifiers.metaKey) {
      const merged = new Map(selected.map((item) => [item.path, item]));
      for (const item of range) merged.set(item.path, item);
      return { selected: Array.from(merged.values()), lastIndex: index };
    }
    return { selected: range, lastIndex: index };
  }
  if (modifiers.ctrlKey || modifiers.metaKey) {
    const exists = selected.some((item) => item.path === entry.path);
    return {
      selected: exists ? selected.filter((item) => item.path !== entry.path) : [...selected, entry],
      lastIndex: index,
    };
  }
  return { selected: [entry], lastIndex: index };
}

export function invertFileSelection(entries, selected) {
  const selectedPaths = new Set(selected.map((item) => item.path));
  return entries.filter((item) => !selectedPaths.has(item.path));
}
