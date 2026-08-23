export async function scanDroppedItems(dataTransfer) {
  const directories = [];
  const files = [];
  const items = Array.from(dataTransfer?.items || []).filter((item) => item.kind === "file");

  for (const item of items) {
    let scanned = false;
    if (typeof item.getAsFileSystemHandle === "function") {
      try {
        const handle = await item.getAsFileSystemHandle();
        if (handle) {
          await scanHandle(handle, "", directories, files);
          scanned = true;
        }
      } catch { /* Fall through to the WebKit entry API or plain files. */ }
    }
    if (!scanned && typeof item.webkitGetAsEntry === "function") {
      const entry = item.webkitGetAsEntry();
      if (entry) {
        await scanEntry(entry, "", directories, files);
        scanned = true;
      }
    }
    if (!scanned) {
      const file = item.getAsFile();
      if (file) files.push({ path: cleanRelativePath(file.name), file });
    }
  }

  if (!items.length) {
    for (const file of Array.from(dataTransfer?.files || [])) {
      const path = cleanRelativePath(file.webkitRelativePath || file.name);
      files.push({ path, file });
    }
  }
  return normalizeScannedItems(directories, files);
}

export function plainUploadItems(files) {
  return normalizeScannedItems([], Array.from(files || []).map((file) => ({
    path: cleanRelativePath(file.webkitRelativePath || file.name),
    file,
  })));
}

export function isExternalFileDrag(event) {
  return Array.from(event.dataTransfer?.types || []).includes("Files");
}

async function scanHandle(handle, parent, directories, files) {
  const current = cleanRelativePath(parent ? parent + "/" + handle.name : handle.name);
  if (handle.kind === "directory") {
    directories.push(current);
    for await (const child of handle.values()) {
      await scanHandle(child, current, directories, files);
    }
    return;
  }
  if (handle.kind === "file") {
    files.push({ path: current, file: await handle.getFile() });
  }
}

async function scanEntry(entry, parent, directories, files) {
  const current = cleanRelativePath(parent ? parent + "/" + entry.name : entry.name);
  if (entry.isDirectory) {
    directories.push(current);
    const reader = entry.createReader();
    for (const child of await readAllEntries(reader)) {
      await scanEntry(child, current, directories, files);
    }
    return;
  }
  if (entry.isFile) {
    const file = await new Promise((resolve, reject) => entry.file(resolve, reject));
    files.push({ path: current, file });
  }
}

async function readAllEntries(reader) {
  const entries = [];
  while (true) {
    const batch = await new Promise((resolve, reject) => reader.readEntries(resolve, reject));
    if (!batch.length) return entries;
    entries.push(...batch);
  }
}

function normalizeScannedItems(directories, files) {
  const directorySet = new Set();
  const normalizedFiles = [];
  for (const directory of directories) {
    addDirectoryAndParents(directorySet, cleanRelativePath(directory));
  }
  for (const item of files) {
    const path = cleanRelativePath(item.path);
    if (!path) continue;
    addDirectoryAndParents(directorySet, parentPath(path));
    normalizedFiles.push({ path, file: item.file });
  }
  return {
    directories: Array.from(directorySet).sort((left, right) => (
      pathDepth(left) - pathDepth(right) || left.localeCompare(right)
    )),
    files: normalizedFiles.sort((left, right) => left.path.localeCompare(right.path)),
  };
}

function addDirectoryAndParents(result, value) {
  let current = value;
  const parents = [];
  while (current) {
    parents.push(current);
    current = parentPath(current);
  }
  for (let index = parents.length - 1; index >= 0; index -= 1) {
    result.add(parents[index]);
  }
}

function parentPath(value) {
  const index = value.lastIndexOf("/");
  return index < 0 ? "" : value.slice(0, index);
}

function pathDepth(value) {
  return value ? value.split("/").length : 0;
}

function cleanRelativePath(value) {
  const parts = String(value || "").replaceAll(String.fromCharCode(92), "/").split("/");
  const result = [];
  for (const part of parts) {
    if (!part || part === ".") continue;
    if (part === ".." || part.includes(String.fromCharCode(0))) {
      throw new Error("拖入内容包含无效路径");
    }
    result.push(part);
  }
  return result.join("/");
}
