export type UploadEntry = { path: string; file?: File };

export function pickedEntries(files: FileList | null): UploadEntry[] {
  return Array.from(files ?? [], (file) => ({
    path: file.webkitRelativePath || file.name,
    file,
  }));
}

export async function droppedEntries(
  data: DataTransfer,
): Promise<UploadEntry[]> {
  const entries = Array.from(data.items, (item) =>
    item.kind === 'file' ? item.webkitGetAsEntry?.() : null,
  ).filter((entry) => entry !== null && entry !== undefined);
  if (!entries.length) return pickedEntries(data.files);
  const files: UploadEntry[] = [];
  async function visit(entry: FileSystemEntry) {
    const path = entry.fullPath.slice(1);
    if (entry.isFile) {
      const file = await new Promise<File>((resolve, reject) =>
        (entry as FileSystemFileEntry).file(resolve, reject),
      );
      files.push({ path, file });
      return;
    }
    const reader = (entry as FileSystemDirectoryEntry).createReader();
    let batch = await new Promise<FileSystemEntry[]>((resolve, reject) =>
      reader.readEntries(resolve, reject),
    );
    if (!batch.length) files.push({ path });
    while (batch.length) {
      for (const child of batch) await visit(child);
      batch = await new Promise<FileSystemEntry[]>((resolve, reject) =>
        reader.readEntries(resolve, reject),
      );
    }
  }
  for (const entry of entries) await visit(entry);
  return files;
}
