import { defineConfig, mergeConfig, type Plugin } from "vite";
import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";
import app from "./vite.config.ts";

// The demo build: Agentry's own pages on a pretend server in the browser
// (src/demo), as static files that work from any folder of any site. It
// swaps src/lib/host.ts for src/demo/host.tsx, so only this build holds the
// demo, and it lands in dist-demo, never where the Go binary embeds the page.
// `npm run build:demo`.
export default mergeConfig(
  app,
  defineConfig({
    base: "./",
    plugins: [noInstall(), noHeavyParts()],
    resolve: {
      alias: [
        { find: /^@\/lib\/host$/, replacement: demo("host.tsx") },
        // xterm, Monaco and the QR encoder are large, and the demo has no
        // sessions, files to edit or devices: plain stand-ins take their place.
        { find: /^(?:@\/components\/organisms|\.)\/TerminalView$/, replacement: demo("TerminalView.tsx") },
        { find: /^(?:@\/components\/molecules|\.)\/(MonacoView|MonacoDiff|MonacoColorize)$/, replacement: demo("$1.tsx") },
        { find: /^(?:@\/components\/atoms|\.)\/QRCode$/, replacement: demo("QRCode.tsx") },
      ],
    },
    build: { outDir: "dist-demo", emptyOutDir: true, copyPublicDir: false },
  }),
);

function demo(file: string): string {
  return fileURLToPath(new URL(`./src/demo/${file}`, import.meta.url));
}

// noInstall leaves out what makes the page an app, the manifest, the service
// worker and the install icons, and the links to them: the demo can't be
// installed. Only the tab's icon ships, whatever the output folder.
function noInstall(): Plugin {
  return {
    name: "demo-no-install",
    transformIndexHtml: (html) => html.replace(/\s*<link rel="(manifest|apple-touch-icon)"[^>]*>/g, ""),
    generateBundle() {
      this.emitFile({
        type: "asset",
        fileName: "icons/icon.svg",
        source: readFileSync(new URL("./public/icons/icon.svg", import.meta.url)),
      });
    },
  };
}

// noHeavyParts fails the build if xterm, Monaco or the QR encoder got in
// anyway, as through an import the stand-ins' aliases don't match.
function noHeavyParts(): Plugin {
  const heavy = /\/node_modules\/(@xterm\/|monaco-editor\/|qrcode-generator\/)/;
  return {
    name: "demo-no-heavy-parts",
    generateBundle(_, bundle) {
      for (const chunk of Object.values(bundle))
        for (const id of chunk.type === "chunk" ? Object.keys(chunk.modules) : [])
          if (heavy.test(id)) this.error(`The demo must not load ${id}: alias its importer to a stand-in in src/demo.`);
    },
  };
}
