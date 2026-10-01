import { Toast } from "@base-ui/react/toast";
import { createRootRoute, Link, Outlet } from "@tanstack/react-router";
import { UploadQueue } from "../components/UploadQueue";
import { cx, Toasts, toastManager } from "../components/ui";

export const Route = createRootRoute({
  component: RootLayout,
  notFoundComponent: () => <p className="text-sm text-neutral-500">Page not found.</p>,
});

const navLink =
  "rounded-md px-3 py-1.5 text-sm font-medium text-neutral-600 hover:bg-neutral-200/70 hover:text-neutral-900 dark:text-neutral-400 dark:hover:bg-neutral-800 dark:hover:text-neutral-100";

function RootLayout() {
  return (
    <Toast.Provider toastManager={toastManager}>
      <div className="flex min-h-dvh flex-col">
        <header className="sticky top-0 z-30 border-b border-neutral-200 bg-white/80 backdrop-blur dark:border-neutral-800 dark:bg-neutral-950/80">
          <div className="mx-auto flex h-14 max-w-5xl items-center gap-6 px-4">
            <Link to="/" className="flex items-center gap-2 font-semibold">
              <span className="grid size-7 place-items-center rounded-md bg-indigo-600 text-white">
                <svg
                  viewBox="0 0 16 16"
                  className="size-3.5"
                  fill="currentColor"
                  aria-hidden="true"
                >
                  <path d="M5 3.5v9l7.5-4.5z" />
                </svg>
              </span>
              just-dlna
            </Link>
            <nav className="flex gap-1">
              {(
                [
                  ["/files", "Files"],
                  ["/settings", "Settings"],
                ] as const
              ).map(([to, label]) => (
                <Link
                  key={to}
                  to={to}
                  className={navLink}
                  activeProps={{
                    className: cx(
                      navLink,
                      "bg-neutral-200/70 text-neutral-900 dark:bg-neutral-800 dark:text-neutral-100",
                    ),
                  }}
                >
                  {label}
                </Link>
              ))}
            </nav>
          </div>
        </header>
        <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-6 pb-32">
          <Outlet />
        </main>
      </div>
      <UploadQueue />
      <Toasts />
    </Toast.Provider>
  );
}
