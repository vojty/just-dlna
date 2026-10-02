import { QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import { queries, queryClient } from "./queries";
import { routeTree } from "./routeTree.gen";
import { onUploadDone } from "./uploads";

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: "intent",
  // Caching is up to TanStack Query; always run loaders so they can consult it.
  defaultPreloadStaleTime: 0,
  defaultPendingMs: 300,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

// Show newly uploaded files in their folder.
onUploadDone((u) => void queryClient.invalidateQueries(queries.files(u.dir)));

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
