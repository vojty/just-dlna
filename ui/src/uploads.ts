// Upload queue kept outside React so uploads continue while navigating.
import { useSyncExternalStore } from "react";
import { ApiError, errorText, uploadFile } from "./api.ts";

type UploadStatus = "queued" | "uploading" | "done" | "error" | "cancelled" | "conflict";

export interface Upload {
  id: number;
  file: File;
  /** Target folder. */
  dir: string;
  loaded: number;
  status: UploadStatus;
  error?: string;
  overwrite: boolean;
}

const CONCURRENCY = 2;

let uploads: Upload[] = [];
let nextId = 1;
const controllers = new Map<number, AbortController>();
const listeners = new Set<() => void>();
const doneListeners = new Set<(u: Upload) => void>();

function emit() {
  for (const l of listeners) l();
}

function update(id: number, patch: Partial<Upload>) {
  uploads = uploads.map((u) => (u.id === id ? { ...u, ...patch } : u));
  emit();
}

function pump() {
  const running = uploads.filter((u) => u.status === "uploading").length;
  const next = uploads.filter((u) => u.status === "queued").slice(0, CONCURRENCY - running);
  for (const u of next) start(u);
}

function start(u: Upload) {
  const controller = new AbortController();
  controllers.set(u.id, controller);
  update(u.id, { status: "uploading", loaded: 0, error: undefined });
  let lastEmit = 0;
  uploadFile(u.dir, u.file, {
    overwrite: u.overwrite,
    signal: controller.signal,
    onProgress: (loaded) => {
      // Progress events fire very often; re-render at most ~10 times a second.
      const now = performance.now();
      if (now - lastEmit > 100) {
        lastEmit = now;
        update(u.id, { loaded });
      }
    },
  })
    .then(() => {
      update(u.id, { status: "done", loaded: u.file.size });
      const done = uploads.find((x) => x.id === u.id);
      if (done) for (const l of doneListeners) l(done);
    })
    .catch((err: unknown) => {
      if (err instanceof DOMException && err.name === "AbortError") {
        update(u.id, { status: "cancelled" });
      } else if (err instanceof ApiError && err.status === 409) {
        update(u.id, { status: "conflict", error: err.message });
      } else {
        update(u.id, { status: "error", error: errorText(err) });
      }
    })
    .finally(() => {
      controllers.delete(u.id);
      pump();
    });
}

/**
 * Queues files for upload into dir. Files whose name is in existing are not
 * sent until the user chooses to overwrite them.
 */
export function enqueue(dir: string, files: Iterable<File>, existing: Set<string> = new Set()) {
  const added: Upload[] = [];
  for (const file of files) {
    const conflict = existing.has(file.name);
    added.push({
      id: nextId++,
      file,
      dir,
      loaded: 0,
      status: conflict ? "conflict" : "queued",
      error: conflict ? `"${file.name}" already exists` : undefined,
      overwrite: false,
    });
  }
  uploads = [...uploads, ...added];
  emit();
  pump();
}

export function cancel(id: number) {
  const c = controllers.get(id);
  if (c) c.abort();
  else update(id, { status: "cancelled" });
}

export function retry(id: number, overwrite = false) {
  update(id, { status: "queued", overwrite, loaded: 0, error: undefined });
  pump();
}

export function dismiss(id: number) {
  cancel(id);
  uploads = uploads.filter((u) => u.id !== id);
  emit();
}

export function clearFinished() {
  uploads = uploads.filter((u) => u.status === "queued" || u.status === "uploading");
  emit();
}

/** Calls fn whenever an upload finishes successfully. Returns an unsubscribe function. */
export function onUploadDone(fn: (u: Upload) => void): () => void {
  doneListeners.add(fn);
  return () => doneListeners.delete(fn);
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function useUploads(): Upload[] {
  return useSyncExternalStore(subscribe, () => uploads);
}
