import { Menu } from "@base-ui/react/menu";
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useRef, useState, type FormEvent } from "react";
import {
  api,
  errorText,
  formatSize,
  joinPath,
  parentPath,
  type FileEntry,
  type FileKind,
} from "../api";
import {
  Button,
  buttonClass,
  Confirm,
  cx,
  Icon,
  inputClass,
  Modal,
  notify,
  notifyError,
} from "../components/ui";
import { queries } from "../queries";
import { enqueue } from "../uploads";

const dateFormat = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});

interface FilesSearch {
  path?: string;
}

export const Route = createFileRoute("/files")({
  validateSearch: (search: Record<string, unknown>): FilesSearch => ({
    path: typeof search.path === "string" && search.path !== "." ? search.path : undefined,
  }),
  loaderDeps: ({ search }) => ({ path: search.path ?? "." }),
  loader: ({ context, deps }) => context.queryClient.ensureQueryData(queries.files(deps.path)),
  component: FilesPage,
  errorComponent: ({ error }) => (
    <div className="flex flex-col items-start gap-3">
      <p className="text-sm text-red-600 dark:text-red-400">{errorText(error)}</p>
      <Link to="/files" className={buttonClass("secondary")}>
        Back to the media folder
      </Link>
    </div>
  ),
});

type Action = { kind: "mkdir" } | { kind: "rename" | "move" | "delete"; entry: FileEntry } | null;

/** A mutation of the media folder that refreshes every folder listing after it succeeds. */
function useFileMutation<T>(mutationFn: (vars: T) => Promise<unknown>) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["files"] }),
    onError: (err) => notifyError(err),
  });
}

function FilesPage() {
  const { path } = Route.useLoaderDeps();
  const { data: listing } = useSuspenseQuery(queries.files(path));
  const dir = listing.path;
  const [action, setAction] = useState<Action>(null);
  const [dragging, setDragging] = useState(false);
  const dragDepth = useRef(0);
  const fileInput = useRef<HTMLInputElement>(null);

  const mkdir = useFileMutation(api.mkdir);
  const move = useFileMutation(({ from, to }: { from: string; to: string }) => api.move(from, to));
  const remove = useFileMutation(api.remove);

  const close = () => setAction(null);
  const done = (success: string) => ({
    onSuccess: () => {
      notify(success);
      close();
    },
  });

  function upload(files: FileList | null) {
    if (!files || files.length === 0) return;
    const existing = new Set(listing.entries.map((e) => e.name));
    enqueue(dir, Array.from(files), existing);
  }

  return (
    <div
      className="relative flex flex-col gap-4"
      onDragEnter={(e) => {
        if (!e.dataTransfer.types.includes("Files")) return;
        dragDepth.current++;
        setDragging(true);
      }}
      onDragLeave={() => {
        dragDepth.current = Math.max(0, dragDepth.current - 1);
        if (dragDepth.current === 0) setDragging(false);
      }}
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes("Files")) e.preventDefault();
      }}
      onDrop={(e) => {
        e.preventDefault();
        dragDepth.current = 0;
        setDragging(false);
        upload(e.dataTransfer.files);
      }}
    >
      <div className="flex flex-wrap items-center gap-3">
        <Breadcrumbs path={dir} />
        <div className="ml-auto flex gap-2">
          <Button onClick={() => setAction({ kind: "mkdir" })}>
            <Icon name="plus" />
            New folder
          </Button>
          <Button variant="primary" onClick={() => fileInput.current?.click()}>
            <Icon name="upload" />
            Upload
          </Button>
          <input
            ref={fileInput}
            type="file"
            multiple
            hidden
            onChange={(e) => {
              upload(e.currentTarget.files);
              e.currentTarget.value = "";
            }}
          />
        </div>
      </div>

      <div className="overflow-hidden rounded-xl border border-neutral-200 bg-white dark:border-neutral-800 dark:bg-neutral-900">
        {listing.entries.length === 0 ? (
          <div className="flex flex-col items-center gap-2 px-4 py-16 text-center text-sm text-neutral-500">
            <Icon name="upload" className="size-8 text-neutral-300 dark:text-neutral-600" />
            This folder is empty. Drop files here or use Upload.
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead className="border-b border-neutral-200 text-left text-xs text-neutral-500 dark:border-neutral-800">
              <tr>
                <th className="px-4 py-2 font-medium">Name</th>
                <th className="hidden w-28 px-4 py-2 text-right font-medium sm:table-cell">Size</th>
                <th className="hidden w-44 px-4 py-2 font-medium md:table-cell">Modified</th>
                <th className="w-12" aria-label="Actions" />
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-100 dark:divide-neutral-800">
              {listing.entries.map((entry) => (
                <tr key={entry.path} className="hover:bg-neutral-50 dark:hover:bg-neutral-800/50">
                  <td className="max-w-0 px-4 py-2">
                    <EntryName entry={entry} />
                  </td>
                  <td className="hidden px-4 py-2 text-right text-neutral-500 tabular-nums sm:table-cell">
                    {entry.isDir ? "—" : formatSize(entry.size)}
                  </td>
                  <td className="hidden px-4 py-2 whitespace-nowrap text-neutral-500 tabular-nums md:table-cell">
                    {dateFormat.format(new Date(entry.modTime))}
                  </td>
                  <td className="px-2 py-1 text-right">
                    <EntryMenu entry={entry} onAction={setAction} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {dragging && (
        <div className="pointer-events-none absolute inset-0 grid place-items-center rounded-xl border-2 border-dashed border-indigo-500 bg-indigo-50/80 text-sm font-medium text-indigo-700 dark:bg-indigo-950/70 dark:text-indigo-300">
          Drop files to upload to {dir === "." ? "the media folder" : dir}
        </div>
      )}

      {action?.kind === "mkdir" && (
        <NameDialog
          onOpenChange={(o) => !o && close()}
          title="New folder"
          description={`In ${dir === "." ? "the media folder" : dir}`}
          submitLabel="Create"
          initial=""
          busy={mkdir.isPending}
          onSubmit={(name) => mkdir.mutate(joinPath(dir, name), done(`Folder "${name}" created`))}
        />
      )}
      {action?.kind === "rename" && (
        <NameDialog
          onOpenChange={(o) => !o && close()}
          title={`Rename ${action.entry.isDir ? "folder" : "file"}`}
          description={action.entry.name}
          submitLabel="Rename"
          initial={action.entry.name}
          busy={move.isPending}
          onSubmit={(name) =>
            move.mutate(
              { from: action.entry.path, to: joinPath(parentPath(action.entry.path), name) },
              done(`Renamed to "${name}"`),
            )
          }
        />
      )}
      {action?.kind === "move" && (
        <MoveDialog
          entry={action.entry}
          onClose={close}
          busy={move.isPending}
          onMove={(target) =>
            move.mutate(
              { from: action.entry.path, to: joinPath(target, action.entry.name) },
              done(`Moved "${action.entry.name}"`),
            )
          }
        />
      )}
      <Confirm
        open={action?.kind === "delete"}
        onOpenChange={(o) => !o && close()}
        title={`Delete ${action?.kind === "delete" && action.entry.isDir ? "folder" : "file"}?`}
        description={
          action?.kind === "delete"
            ? action.entry.isDir
              ? `"${action.entry.name}" and everything in it will be deleted permanently.`
              : `"${action.entry.name}" will be deleted permanently.`
            : ""
        }
        confirmLabel="Delete"
        onConfirm={() => {
          if (action?.kind === "delete") {
            const { entry } = action;
            remove.mutate(entry.path, done(`Deleted "${entry.name}"`));
          }
        }}
      />
    </div>
  );
}

function Breadcrumbs({ path }: { path: string }) {
  const parts = path === "." ? [] : path.split("/");
  return (
    <nav aria-label="Folder" className="flex min-w-0 flex-wrap items-center gap-1 text-sm">
      <Link
        to="/files"
        className="flex items-center gap-1.5 rounded px-1.5 py-1 font-medium hover:bg-neutral-200/70 dark:hover:bg-neutral-800"
      >
        <Icon name="home" />
        Media
      </Link>
      {parts.map((part, i) => {
        const to = parts.slice(0, i + 1).join("/");
        const last = i === parts.length - 1;
        return (
          <span key={to} className="flex min-w-0 items-center gap-1">
            <Icon name="chevron" className="text-neutral-400" />
            {last ? (
              <span className="truncate px-1.5 py-1 font-medium" aria-current="page">
                {part}
              </span>
            ) : (
              <Link
                to="/files"
                search={{ path: to }}
                className="truncate rounded px-1.5 py-1 hover:bg-neutral-200/70 dark:hover:bg-neutral-800"
              >
                {part}
              </Link>
            )}
          </span>
        );
      })}
    </nav>
  );
}

const kindIcon: Record<FileKind, "folder" | "video" | "subtitle" | "file"> = {
  folder: "folder",
  video: "video",
  subtitle: "subtitle",
  other: "file",
};

const kindColor: Record<FileKind, string> = {
  folder: "text-amber-500",
  video: "text-indigo-500",
  subtitle: "text-emerald-500",
  other: "text-neutral-400",
};

function EntryName({ entry }: { entry: FileEntry }) {
  const content = (
    <>
      <Icon name={kindIcon[entry.kind]} className={cx("size-5", kindColor[entry.kind])} />
      <span className="truncate">{entry.name}</span>
    </>
  );
  return entry.isDir ? (
    <Link
      to="/files"
      search={{ path: entry.path }}
      className="flex items-center gap-2.5 font-medium hover:text-indigo-600 dark:hover:text-indigo-400"
    >
      {content}
    </Link>
  ) : (
    <span className="flex items-center gap-2.5" title={entry.name}>
      {content}
    </span>
  );
}

const menuItem =
  "flex cursor-default items-center gap-2 rounded px-3 py-1.5 text-sm outline-none select-none data-highlighted:bg-neutral-100 dark:data-highlighted:bg-neutral-800";

function EntryMenu({
  entry,
  onAction,
}: {
  entry: FileEntry;
  onAction: (a: Exclude<Action, null>) => void;
}) {
  return (
    <Menu.Root>
      <Menu.Trigger
        aria-label={`Actions for ${entry.name}`}
        className={buttonClass("ghost", "h-8 w-8 px-0 data-popup-open:bg-neutral-200/70")}
      >
        <Icon name="more" />
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner sideOffset={4} align="end" className="z-40 outline-none">
          <Menu.Popup className="min-w-40 origin-(--transform-origin) rounded-lg border border-neutral-200 bg-white p-1 shadow-lg transition-[scale,opacity] duration-100 data-ending-style:scale-95 data-ending-style:opacity-0 data-starting-style:scale-95 data-starting-style:opacity-0 dark:border-neutral-800 dark:bg-neutral-900">
            <Menu.Item className={menuItem} onClick={() => onAction({ kind: "rename", entry })}>
              Rename…
            </Menu.Item>
            <Menu.Item className={menuItem} onClick={() => onAction({ kind: "move", entry })}>
              Move…
            </Menu.Item>
            {!entry.isDir && (
              <Menu.Item
                className={menuItem}
                // The server sends Content-Disposition: attachment, so the
                // page stays open.
                onClick={() => window.location.assign(api.downloadUrl(entry.path))}
              >
                Download
              </Menu.Item>
            )}
            <Menu.Separator className="my-1 h-px bg-neutral-200 dark:bg-neutral-800" />
            <Menu.Item
              className={cx(menuItem, "text-red-600 dark:text-red-400")}
              onClick={() => onAction({ kind: "delete", entry })}
            >
              Delete…
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** A dialog asking for a file or folder name; mount it only while open. */
function NameDialog({
  onOpenChange,
  title,
  description,
  submitLabel,
  initial,
  busy,
  onSubmit,
}: {
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  submitLabel: string;
  initial: string;
  busy: boolean;
  onSubmit: (name: string) => void;
}) {
  const [name, setName] = useState(initial);

  const trimmed = name.trim();
  const invalid = trimmed.includes("/") || trimmed.includes("\\") || trimmed.startsWith(".");

  function submit(e: FormEvent) {
    e.preventDefault();
    if (busy || !trimmed || invalid || trimmed === initial) return;
    onSubmit(trimmed);
  }

  return (
    <Modal open onOpenChange={onOpenChange} title={title} description={description}>
      <form onSubmit={submit} className="flex flex-col gap-4">
        <div className="flex flex-col gap-1">
          <input
            aria-label="Name"
            className={inputClass}
            value={name}
            onChange={(e) => setName(e.target.value)}
            onFocus={(e) => {
              // Select the name without its extension, like file managers do.
              const dot = e.currentTarget.value.lastIndexOf(".");
              e.currentTarget.setSelectionRange(0, dot > 0 ? dot : e.currentTarget.value.length);
            }}
          />
          {invalid && (
            <p className="text-xs text-red-600 dark:text-red-400">
              Names cannot contain slashes or start with a dot.
            </p>
          )}
        </div>
        <div className="flex justify-end gap-2">
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={busy || !trimmed || invalid || trimmed === initial}
          >
            {submitLabel}
          </Button>
        </div>
      </form>
    </Modal>
  );
}

/** Lets the user pick a target folder by browsing the media folder. */
function MoveDialog({
  entry,
  busy,
  onClose,
  onMove,
}: {
  entry: FileEntry;
  busy: boolean;
  onClose: () => void;
  onMove: (targetDir: string) => void;
}) {
  const current = parentPath(entry.path);
  const [dir, setDir] = useState(current);
  const { data: folders, error } = useQuery({
    ...queries.files(dir),
    select: (l) => l.entries.filter((e) => e.isDir && e.path !== entry.path),
  });

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Move "${entry.name}"`}
      description="Choose the folder to move it to."
    >
      <div className="flex flex-col overflow-hidden rounded-lg border border-neutral-200 dark:border-neutral-800">
        <div className="flex items-center gap-2 border-b border-neutral-200 bg-neutral-50 px-3 py-2 text-sm dark:border-neutral-800 dark:bg-neutral-950">
          <Button
            variant="ghost"
            className="h-7 px-2"
            disabled={dir === "."}
            onClick={() => setDir(parentPath(dir))}
            aria-label="Up one folder"
          >
            ↑
          </Button>
          <span className="truncate font-medium">{dir === "." ? "Media" : `Media/${dir}`}</span>
        </div>
        <ul className="h-56 overflow-y-auto p-1">
          {error ? (
            <li className="px-3 py-2 text-sm text-red-600 dark:text-red-400">{errorText(error)}</li>
          ) : !folders ? (
            <li className="px-3 py-2 text-sm text-neutral-500">Loading…</li>
          ) : folders.length === 0 ? (
            <li className="px-3 py-2 text-sm text-neutral-500">No sub-folders</li>
          ) : (
            folders.map((f) => (
              <li key={f.path}>
                <button
                  type="button"
                  className="flex w-full items-center gap-2 rounded px-3 py-1.5 text-left text-sm hover:bg-neutral-100 dark:hover:bg-neutral-800"
                  onClick={() => setDir(f.path)}
                >
                  <Icon name="folder" className="text-amber-500" />
                  <span className="truncate">{f.name}</span>
                </button>
              </li>
            ))
          )}
        </ul>
      </div>
      <div className="flex justify-end gap-2">
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={busy || dir === current} onClick={() => onMove(dir)}>
          Move here
        </Button>
      </div>
    </Modal>
  );
}
