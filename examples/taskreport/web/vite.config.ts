import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { tanstackRouter } from "@tanstack/router-plugin/vite";

export default defineConfig({
  // The router plugin generates routeTree.gen.ts from the files in
  // src/routes, so adding a page is adding a file.
  plugins: [tanstackRouter({ target: "react", autoCodeSplitting: true }), react()],
  build: {
    // Served by the Go binary from an embedded FS, so the output goes where
    // web/embed.go expects it.
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    // During development the Go process still owns the data and the model;
    // only the assets come from Vite.
    proxy: { "/api": "http://localhost:8080" },
  },
});
