import { Progress } from "@base-ui/react/progress";
import { useState } from "react";
import { formatSize } from "../api";
import { cancel, clearFinished, dismiss, retry, useUploads, type Upload } from "../uploads";
import { Button, cx, Icon } from "./ui";

/** Floating panel listing uploads with their progress. */
export function UploadQueue() {
  const uploads = useUploads();
  const [collapsed, setCollapsed] = useState(false);
  if (uploads.length === 0) return null;

  const active = uploads.filter((u) => u.status === "queued" || u.status === "uploading");
  const total = active.reduce((n, u) => n + u.file.size, 0);
  const loaded = active.reduce((n, u) => n + u.loaded, 0);
  const heading =
    active.length > 0
      ? `Uploading ${active.length} file${active.length === 1 ? "" : "s"} · ${total ? Math.floor((loaded / total) * 100) : 0}%`
      : "Uploads";

  return (
    <section
      aria-label="Uploads"
      className="fixed bottom-4 left-4 z-40 flex max-h-[60vh] w-96 max-w-[calc(100vw-2rem)] flex-col overflow-hidden rounded-xl border border-neutral-200 bg-white shadow-xl dark:border-neutral-800 dark:bg-neutral-900"
    >
      <header className="flex items-center gap-2 border-b border-neutral-200 py-2 pr-2 pl-4 dark:border-neutral-800">
        <h2 className="flex-1 text-sm font-semibold">{heading}</h2>
        {active.length < uploads.length && (
          <Button variant="ghost" className="h-7 px-2 text-xs" onClick={clearFinished}>
            Clear finished
          </Button>
        )}
        <Button
          variant="ghost"
          className="h-7 px-2 text-xs"
          aria-expanded={!collapsed}
          onClick={() => setCollapsed((c) => !c)}
        >
          {collapsed ? "Show" : "Hide"}
        </Button>
      </header>
      {!collapsed && (
        <ul className="divide-y divide-neutral-100 overflow-y-auto dark:divide-neutral-800">
          {uploads.map((u) => (
            <UploadRow key={u.id} upload={u} />
          ))}
        </ul>
      )}
    </section>
  );
}

const statusText: Record<Upload["status"], string> = {
  queued: "Waiting…",
  uploading: "",
  done: "Done",
  error: "Failed",
  cancelled: "Cancelled",
  conflict: "Already exists",
};

function UploadRow({ upload: u }: { upload: Upload }) {
  const pct = u.file.size ? Math.floor((u.loaded / u.file.size) * 100) : 100;
  const progressValue = u.status === "queued" ? null : u.status === "done" ? 100 : pct;
  return (
    <li className="flex flex-col gap-1.5 px-4 py-2.5">
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 truncate text-sm" title={u.file.name}>
          {u.file.name}
        </span>
        <span
          className={cx(
            "text-xs whitespace-nowrap tabular-nums",
            u.status === "error" || u.status === "conflict"
              ? "text-red-600 dark:text-red-400"
              : u.status === "done"
                ? "text-emerald-600 dark:text-emerald-400"
                : "text-neutral-500",
          )}
        >
          {u.status === "uploading"
            ? `${formatSize(u.loaded)} / ${formatSize(u.file.size)} · ${pct}%`
            : statusText[u.status]}
        </span>
        <RowActions upload={u} />
      </div>
      {(u.status === "uploading" || u.status === "queued" || u.status === "done") && (
        <Progress.Root value={progressValue} aria-label={`Upload of ${u.file.name}`}>
          <Progress.Track className="h-1.5 overflow-hidden rounded-full bg-neutral-200 dark:bg-neutral-800">
            <Progress.Indicator
              className={cx(
                "h-full rounded-full transition-[width] duration-200",
                u.status === "done" ? "bg-emerald-500" : "bg-indigo-500",
              )}
            />
          </Progress.Track>
        </Progress.Root>
      )}
      {u.error && u.status !== "conflict" && (
        <p className="text-xs text-red-600 dark:text-red-400">{u.error}</p>
      )}
    </li>
  );
}

function RowActions({ upload: u }: { upload: Upload }) {
  const small = "h-6 px-2 text-xs";
  switch (u.status) {
    case "queued":
    case "uploading":
      return (
        <Button variant="ghost" className={small} onClick={() => cancel(u.id)}>
          Cancel
        </Button>
      );
    case "conflict":
      return (
        <>
          <Button variant="secondary" className={small} onClick={() => retry(u.id, true)}>
            Overwrite
          </Button>
          <Button variant="ghost" className={small} onClick={() => dismiss(u.id)}>
            Skip
          </Button>
        </>
      );
    case "error":
    case "cancelled":
      return (
        <>
          <Button variant="secondary" className={small} onClick={() => retry(u.id, u.overwrite)}>
            Retry
          </Button>
          <Button
            variant="ghost"
            className="h-6 px-1"
            aria-label="Remove"
            onClick={() => dismiss(u.id)}
          >
            <Icon name="close" />
          </Button>
        </>
      );
    case "done":
      return (
        <Button
          variant="ghost"
          className="h-6 px-1"
          aria-label="Remove"
          onClick={() => dismiss(u.id)}
        >
          <Icon name="close" />
        </Button>
      );
  }
}
