import { defineConfig } from "vite";

// Tauri expects a fixed dev port and must not clear the terminal output.
export default defineConfig({
  clearScreen: false,
  server: { port: 1420, strictPort: true },
  build: { target: "es2021", outDir: "dist", emptyOutDir: true },
});
