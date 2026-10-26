import { defineConfig } from "vite";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  // Relative asset base: the packaged app loads dist/index.html over
  // file://, where absolute /assets/... paths resolve to the fs root.
  base: "./",
  plugins: [tailwindcss()],
  server: { port: 5173, strictPort: true },
  build: { outDir: "dist", emptyOutDir: true },
});
