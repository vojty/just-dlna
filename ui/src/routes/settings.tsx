import { Field } from "@base-ui/react/field";
import { Select } from "@base-ui/react/select";
import { Switch } from "@base-ui/react/switch";
import { createFileRoute, useRouter } from "@tanstack/react-router";
import { useMemo, useState, type FormEvent } from "react";
import { api, errorText, type Config, type Setting } from "../api";
import { Button, cx, Icon, inputClass, notify, notifyError } from "../components/ui";

export const Route = createFileRoute("/settings")({
  loader: () => api.config(),
  component: SettingsPage,
  errorComponent: ({ error }) => (
    <p className="text-sm text-red-600 dark:text-red-400">{errorText(error)}</p>
  ),
});

const labels: Record<string, string> = {
  path: "Media folder",
  name: "Server name",
  "http-port": "DLNA port",
  "media-port": "Streaming port",
  "ui-port": "Web UI port",
  cache: "Cache folder",
  "sub-charset": "Subtitle charset",
  "prefetch-subs": "Prefetch embedded subtitles",
  "log-level": "Log level",
  "dms-log-level": "DLNA library log level",
  "log-format": "Log format",
  "log-headers": "Log DLNA headers",
  ifname: "Network interface",
  "allowed-ips": "Allowed client networks",
};

const groups: { title: string; names: string[] }[] = [
  { title: "General", names: ["name", "path", "cache"] },
  { title: "Network", names: ["http-port", "media-port", "ui-port", "ifname", "allowed-ips"] },
  { title: "Subtitles", names: ["sub-charset", "prefetch-subs"] },
  { title: "Logging", names: ["log-level", "dms-log-level", "log-format", "log-headers"] },
];

/** The value shown in the form: what the server will use after a restart. */
function savedValue(s: Setting): string {
  if (s.locked) return s.value;
  return s.fileValue ?? s.default;
}

function SettingsPage() {
  const config = Route.useLoaderData();
  // Remount the form with fresh state whenever the saved config changes.
  return <SettingsForm key={JSON.stringify(config)} config={config} />;
}

function SettingsForm({ config }: { config: Config }) {
  const router = useRouter();
  const byName = useMemo(() => new Map(config.settings.map((s) => [s.name, s])), [config.settings]);
  const initial = useMemo(
    () => Object.fromEntries(config.settings.map((s) => [s.name, savedValue(s)])),
    [config.settings],
  );
  const [draft, setDraft] = useState<Record<string, string>>(initial);
  const [saving, setSaving] = useState(false);
  const [restarting, setRestarting] = useState(false);

  const changed = config.settings.filter((s) => !s.locked && draft[s.name] !== initial[s.name]);
  const set = (name: string, value: string) => setDraft((d) => ({ ...d, [name]: value }));

  async function save(e: FormEvent) {
    e.preventDefault();
    const updates: Record<string, string | null> = {};
    for (const s of changed) {
      // Keep the file minimal: a value equal to the default is removed from it.
      updates[s.name] = draft[s.name] === s.default ? null : draft[s.name];
    }
    setSaving(true);
    try {
      await api.saveConfig(updates);
      notify("Settings saved", "success", "Restart the server to apply them.");
      await router.invalidate();
    } catch (err) {
      notifyError(err, "Could not save settings");
    } finally {
      setSaving(false);
    }
  }

  async function restart() {
    const before = await api.health().catch(() => null);
    const uiPort = byName.get("ui-port");
    const newPort = uiPort ? savedValue(uiPort) : "";
    setRestarting(true);
    try {
      await api.restart();
    } catch (err) {
      setRestarting(false);
      notifyError(err, "Could not restart");
      return;
    }
    // The web UI moves along with its port; the Vite dev server (proxy) does not.
    if (uiPort && newPort !== uiPort.value && window.location.port === uiPort.value) {
      const url = new URL(window.location.href);
      url.port = newPort;
      setTimeout(() => window.location.assign(url), 2000);
      return;
    }
    const deadline = Date.now() + 30_000;
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 1000));
      const h = await api.health().catch(() => null);
      if (h && h.startedAt !== before?.startedAt) {
        setRestarting(false);
        notify("Server restarted");
        await router.invalidate();
        return;
      }
    }
    setRestarting(false);
    notify("The server did not come back", "error", "Check the server logs.");
  }

  return (
    <form onSubmit={save} className="flex flex-col gap-6">
      <div className="sticky top-14 z-20 -mx-4 flex flex-wrap items-end justify-between gap-3 border-b border-neutral-200 bg-neutral-50/90 px-4 py-3 backdrop-blur dark:border-neutral-800 dark:bg-neutral-950/90">
        <div>
          <h1 className="text-xl font-semibold">Settings</h1>
          <p className="text-sm break-all text-neutral-500">
            Saved to <code className="text-xs">{config.file}</code>
          </p>
        </div>
        <div className="flex gap-2">
          <Button disabled={changed.length === 0 || saving} onClick={() => setDraft(initial)}>
            Discard
          </Button>
          <Button type="submit" variant="primary" disabled={changed.length === 0 || saving}>
            {saving
              ? "Saving…"
              : changed.length
                ? `Save ${changed.length} change${changed.length === 1 ? "" : "s"}`
                : "Save"}
          </Button>
        </div>
      </div>

      {(config.restartPending || restarting) && (
        <div className="flex flex-wrap items-center gap-3 rounded-xl border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/50 dark:text-amber-200">
          <span className="flex-1">
            {restarting
              ? "Restarting the server…"
              : "Saved settings take effect after the server restarts."}
          </span>
          <Button variant="primary" disabled={restarting} onClick={restart}>
            {restarting ? "Restarting…" : "Restart now"}
          </Button>
        </div>
      )}

      {groups.map((g) => {
        const settings = g.names.map((n) => byName.get(n)).filter((s): s is Setting => !!s);
        if (settings.length === 0) return null;
        return (
          <section
            key={g.title}
            className="rounded-xl border border-neutral-200 bg-white dark:border-neutral-800 dark:bg-neutral-900"
          >
            <h2 className="border-b border-neutral-200 px-5 py-3 text-sm font-semibold dark:border-neutral-800">
              {g.title}
            </h2>
            <div className="divide-y divide-neutral-100 dark:divide-neutral-800">
              {settings.map((s) => (
                <SettingField
                  key={s.name}
                  setting={s}
                  value={draft[s.name] ?? ""}
                  dirty={draft[s.name] !== initial[s.name]}
                  onChange={(v) => set(s.name, v)}
                />
              ))}
            </div>
          </section>
        );
      })}
    </form>
  );
}

function SettingField({
  setting: s,
  value,
  dirty,
  onChange,
}: {
  setting: Setting;
  value: string;
  dirty: boolean;
  onChange: (v: string) => void;
}) {
  const pendingRestart = !s.locked && savedValue(s) !== s.value;
  return (
    <Field.Root
      name={s.name}
      disabled={s.locked}
      className="grid gap-x-6 gap-y-2 px-5 py-4 md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]"
    >
      <div className="flex flex-col gap-1">
        <Field.Label className="flex flex-wrap items-center gap-2 text-sm font-medium">
          {labels[s.name] ?? s.name}
          {dirty && <span className="size-1.5 rounded-full bg-indigo-500" title="Unsaved" />}
        </Field.Label>
        <Field.Description className="text-xs text-neutral-500">
          {s.usage.charAt(0).toUpperCase() + s.usage.slice(1)}. <code>{s.name}</code> /{" "}
          <code>{s.env}</code>
        </Field.Description>
      </div>
      <div className="flex flex-col gap-1.5">
        <Control setting={s} value={value} onChange={onChange} />
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-neutral-500">
          {s.locked ? (
            <span className="flex items-center gap-1 text-amber-700 dark:text-amber-400">
              <Icon name="lock" className="size-3.5" />
              Set by{" "}
              {s.source === "env" ? `the ${s.env} environment variable` : "a command line flag"}
            </span>
          ) : (
            <>
              <span>
                Default: <code>{s.default === "" ? "(empty)" : s.default}</code>
              </span>
              {value !== s.default && (
                <button
                  type="button"
                  className="text-indigo-600 hover:underline dark:text-indigo-400"
                  onClick={() => onChange(s.default)}
                >
                  Reset to default
                </button>
              )}
            </>
          )}
          {pendingRestart && (
            <span className="text-amber-700 dark:text-amber-400">
              Running with <code>{s.value === "" ? "(empty)" : s.value}</code> until restart
            </span>
          )}
        </div>
      </div>
    </Field.Root>
  );
}

function Control({
  setting: s,
  value,
  onChange,
}: {
  setting: Setting;
  value: string;
  onChange: (v: string) => void;
}) {
  switch (s.type) {
    case "bool":
      return (
        <Switch.Root
          checked={value === "true"}
          onCheckedChange={(c) => onChange(String(c))}
          disabled={s.locked}
          className="flex h-6 w-11 shrink-0 rounded-full bg-neutral-300 p-0.5 transition-colors data-checked:bg-indigo-600 data-disabled:opacity-50 dark:bg-neutral-700"
        >
          <Switch.Thumb className="size-5 rounded-full bg-white shadow transition-transform data-checked:translate-x-5" />
        </Switch.Root>
      );
    case "enum":
      return (
        <Select.Root
          value={value}
          onValueChange={(v) => onChange(String(v))}
          disabled={s.locked}
          items={(s.options ?? []).map((o) => ({ value: o, label: o }))}
        >
          <Select.Trigger className={cx(inputClass, "flex items-center justify-between text-left")}>
            <Select.Value />
            <Select.Icon className="text-neutral-400">▾</Select.Icon>
          </Select.Trigger>
          <Select.Portal>
            <Select.Positioner sideOffset={4} className="z-40 outline-none">
              <Select.Popup className="min-w-(--anchor-width) rounded-lg border border-neutral-200 bg-white p-1 shadow-lg dark:border-neutral-800 dark:bg-neutral-900">
                <Select.List>
                  {(s.options ?? []).map((o) => (
                    <Select.Item
                      key={o}
                      value={o}
                      className="flex cursor-default items-center gap-2 rounded px-3 py-1.5 text-sm outline-none select-none data-highlighted:bg-neutral-100 dark:data-highlighted:bg-neutral-800"
                    >
                      <Select.ItemIndicator className="w-3 text-indigo-600">✓</Select.ItemIndicator>
                      <Select.ItemText>{o}</Select.ItemText>
                    </Select.Item>
                  ))}
                </Select.List>
              </Select.Popup>
            </Select.Positioner>
          </Select.Portal>
        </Select.Root>
      );
    case "list":
      // One network per line; stored comma separated.
      return (
        <Field.Control
          render={<textarea rows={3} />}
          className={cx(inputClass, "h-auto py-2 font-mono")}
          value={value.split(",").join("\n")}
          onChange={(e) =>
            onChange(
              e.target.value
                .split("\n")
                .map((l) => l.trim())
                .join(","),
            )
          }
        />
      );
    case "int":
      return (
        <Field.Control
          type="number"
          inputMode="numeric"
          className={cx(inputClass, "max-w-40")}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    default:
      return (
        <Field.Control
          className={inputClass}
          value={value}
          placeholder={s.default === "" ? "(not set)" : s.default}
          onChange={(e) => onChange(e.target.value)}
        />
      );
  }
}
