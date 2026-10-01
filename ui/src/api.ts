// Typed client for the just-dlna admin API (internal/admin).

export type FileKind = "folder" | "video" | "subtitle" | "other";

export interface FileEntry {
  name: string;
  /** Slash-separated path relative to the media folder. */
  path: string;
  isDir: boolean;
  size: number;
  modTime: string;
  kind: FileKind;
}

export interface Listing {
  /** "." for the media folder itself. */
  path: string;
  entries: FileEntry[];
}

export type SettingType = "string" | "int" | "bool" | "enum" | "list";
export type SettingSource = "flag" | "env" | "file" | "default";

export interface Setting {
  name: string;
  env: string;
  usage: string;
  type: SettingType;
  options?: string[];
  /** Value of the running server. */
  value: string;
  /** Value without the config file. */
  default: string;
  /** Value in the config file, if set there. */
  fileValue?: string;
  source: SettingSource;
  /** Set by a command line flag or environment variable. */
  locked: boolean;
}

export interface Config {
  file: string;
  restartPending: boolean;
  settings: Setting[];
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(method: string, url: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(url, {
      method,
      headers: body === undefined ? undefined : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, "Cannot reach the server");
  }
  if (!res.ok) {
    throw new ApiError(res.status, await errorMessage(res));
  }
  return res.status === 204 ? (undefined as T) : ((await res.json()) as T);
}

async function errorMessage(res: Response): Promise<string> {
  try {
    const data = (await res.json()) as { error?: string };
    return data.error ?? res.statusText;
  } catch {
    return res.statusText || `HTTP ${res.status}`;
  }
}

const q = (path: string) => encodeURIComponent(path);

export const api = {
  health: () => request<{ ok: boolean; startedAt: string }>("GET", "/api/health"),
  config: () => request<Config>("GET", "/api/config"),
  /** Saves settings; null removes a setting from the file (back to default). */
  saveConfig: (updates: Record<string, string | null>) =>
    request<Config>("PUT", "/api/config", updates),
  restart: () => request<void>("POST", "/api/restart"),

  list: (path: string) => request<Listing>("GET", `/api/files?path=${q(path)}`),
  mkdir: (path: string) => request<FileEntry>("POST", "/api/files/mkdir", { path }),
  move: (from: string, to: string) => request<FileEntry>("POST", "/api/files/move", { from, to }),
  remove: (path: string) => request<void>("DELETE", `/api/files?path=${q(path)}`),
  downloadUrl: (path: string) => `/api/files/download?path=${q(path)}`,
};

export interface UploadOptions {
  overwrite?: boolean;
  signal?: AbortSignal;
  onProgress?: (loaded: number, total: number) => void;
}

/**
 * Uploads one file into the folder dir. Uses XMLHttpRequest because fetch
 * does not report upload progress.
 */
export function uploadFile(dir: string, file: File, opts: UploadOptions = {}): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const url = `/api/files/upload?path=${q(dir)}${opts.overwrite ? "&overwrite=1" : ""}`;
    xhr.open("POST", url);
    xhr.responseType = "json";
    xhr.upload.addEventListener("progress", (e) => {
      if (e.lengthComputable) opts.onProgress?.(e.loaded, e.total);
    });
    xhr.addEventListener("load", () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
      } else {
        const data = xhr.response as { error?: string } | null;
        reject(new ApiError(xhr.status, data?.error ?? `HTTP ${xhr.status}`));
      }
    });
    xhr.addEventListener("error", () => reject(new ApiError(0, "Upload failed: network error")));
    xhr.addEventListener("abort", () => reject(new DOMException("Upload cancelled", "AbortError")));
    if (opts.signal) {
      if (opts.signal.aborted) {
        reject(new DOMException("Upload cancelled", "AbortError"));
        return;
      }
      opts.signal.addEventListener("abort", () => xhr.abort(), { once: true });
    }
    const form = new FormData();
    form.append("file", file, file.name);
    xhr.send(form);
  });
}

/** Joins a folder path from the API ("." is the root) with a name. */
export function joinPath(dir: string, name: string): string {
  return dir === "." || dir === "" ? name : `${dir}/${name}`;
}

/** Returns the parent folder of a path, "." for top-level entries. */
export function parentPath(path: string): string {
  const i = path.lastIndexOf("/");
  return i < 0 ? "." : path.slice(0, i);
}

export function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}
