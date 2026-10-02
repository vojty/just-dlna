import { defineConfig } from "@hey-api/openapi-ts";

// Generates the UI's API client from the OpenAPI document of the Go server
// (go run ./cmd/openapi). Run scripts/gen-api.sh instead of calling this directly.
export default defineConfig({
  input: "openapi.json",
  output: "ui/src/client",
  plugins: [
    // Same origin: the Go server, or the Vite dev server proxying /api.
    { name: "@hey-api/client-fetch", baseUrl: false },
    "@hey-api/typescript",
    "@hey-api/sdk",
  ],
});
