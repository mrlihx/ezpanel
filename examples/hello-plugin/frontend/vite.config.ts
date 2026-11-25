import { defineConfig } from "vite";
export default defineConfig({
  base: "./",
  build: {
    outDir: "../package/web",
    emptyOutDir: true,
    lib: {
      entry: "src/entry.tsx",
      formats: ["es"],
      fileName: () => "entry.js",
      cssFileName: "entry",
    },
    rolldownOptions: {
      external: [
        "react",
        "react-dom",
        "react-dom/client",
        "react/jsx-runtime",
        "react/jsx-dev-runtime",
        "antd",
        "@ant-design/icons",
      ],
      output: {
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: (asset) =>
          asset.names.includes("entry.css")
            ? "entry.css"
            : "assets/[name]-[hash][extname]",
      },
    },
  },
});
