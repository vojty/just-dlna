// TanStack Query options for the admin API. Route loaders prefetch with them
// (queryClient.ensureQueryData) and components read with useSuspenseQuery.
import { QueryClient, queryOptions } from "@tanstack/react-query";
import { api } from "./api";

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Loaders have just fetched the data; don't fetch it again on mount.
      staleTime: 10_000,
    },
  },
});

export const queries = {
  /** Listing of one folder; invalidate ["files"] to refresh every folder. */
  files: (path: string) =>
    queryOptions({
      queryKey: ["files", path],
      queryFn: () => api.list(path),
    }),
  config: () =>
    queryOptions({
      queryKey: ["config"],
      queryFn: () => api.config(),
    }),
};
