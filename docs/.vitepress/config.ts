import { defineConfig } from "vitepress";
import { withMermaid } from "vitepress-plugin-mermaid";

// withMermaid wraps the config so ```mermaid fences render as diagrams.
export default withMermaid(
  defineConfig({
    title: "Code Mode",
    description:
      "Turn protobuf RPCs into capabilities a model can compose in a sandbox.",
    lang: "en-US",

    // Pages live in content/ so this directory holds only the site: the
    // toolchain that builds it, its config, and its content. Internal writing
    // — PRDs, milestones, decisions — lives in notes/ and is never published.
    srcDir: "content",

    // Serving from a repository subpath needs base: "/<repo>/".
    base: "/",
    cleanUrls: true,
    lastUpdated: true,

    themeConfig: {
      nav: [
        {
          text: "Guide",
          link: "/quickstart",
          activeMatch:
            "^/(quickstart|existing-app|overview|architecture|flow|using-it|environments|dependencies|permissions|tenants|writes|caching|scaling-up|mcp|capabilities|search|models|sandbox|worker)",
        },
        {
          text: "Reference",
          link: "/configuration",
          activeMatch: "^/(configuration|latency|troubleshooting)",
        },
      ],

      sidebar: [
        {
          text: "Get started",
          items: [
            { text: "Quickstart", link: "/quickstart" },
            { text: "Integration guide", link: "/using-it" },
            { text: "Adding it to an existing app", link: "/existing-app" },
          ],
        },
        {
          text: "Ship it",
          items: [
            { text: "Development and production", link: "/environments" },
            { text: "Running the worker", link: "/worker" },
            { text: "What your binary links", link: "/dependencies" },
            { text: "Permissions", link: "/permissions" },
            { text: "Roles and tenants", link: "/tenants" },
            { text: "Writes and retries", link: "/writes" },
            { text: "Caching programs", link: "/caching" },
          ],
        },
        {
          text: "Grow it",
          items: [
            { text: "Many services, many users", link: "/scaling-up" },
            { text: "Serving MCP clients", link: "/mcp" },
          ],
        },
        {
          text: "How it works",
          items: [
            { text: "What code mode is", link: "/overview" },
            { text: "Architecture", link: "/architecture" },
            { text: "The flow, step by step", link: "/flow" },
            { text: "Capabilities", link: "/capabilities" },
            { text: "Search", link: "/search" },
            { text: "Models", link: "/models" },
            { text: "Sandbox", link: "/sandbox" },
          ],
        },
        {
          text: "Reference",
          items: [
            { text: "Configuration", link: "/configuration" },
            { text: "Latency", link: "/latency" },
            { text: "Troubleshooting", link: "/troubleshooting" },
          ],
        },
      ],

      outline: { level: [2, 3], label: "On this page" },
      search: { provider: "local" },

      docFooter: { prev: true, next: true },
    },

    mermaid: {
      // Mermaid picks its own theme; these keep diagrams legible against both
      // VitePress grounds without hard-coding a palette per diagram.
      theme: "default",
      themeVariables: { fontFamily: "inherit" },
    },

    // Pre-bundle mermaid: under pnpm the plugin's own CJS includes (dayjs, ...) do not resolve.
    vite: {
      optimizeDeps: { include: ["mermaid"] },
    },
  }),
);
