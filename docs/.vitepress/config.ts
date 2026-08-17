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
          link: "/overview",
          activeMatch:
            "^/(overview|flow|using-it|dependencies|permissions|caching|scaling-up)",
        },
        {
          text: "Reference",
          link: "/configuration",
          activeMatch: "^/(configuration|latency|troubleshooting)",
        },
      ],

      sidebar: [
        {
          text: "Understanding it",
          items: [
            { text: "What code mode is", link: "/overview" },
            { text: "The flow, step by step", link: "/flow" },
          ],
        },
        {
          text: "Using it",
          items: [
            { text: "Add it to your application", link: "/using-it" },
            { text: "What your binary links", link: "/dependencies" },
            { text: "Permissions", link: "/permissions" },
            { text: "Caching programs", link: "/caching" },
            { text: "Many services, many users", link: "/scaling-up" },
          ],
        },
        {
          text: "The pieces",
          items: [
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
  }),
);
