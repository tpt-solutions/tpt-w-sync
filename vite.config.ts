import { defineConfig } from "vite";
// @ts-expect-error plain ESM helper, no type declarations needed
import { findFreePort } from "./scripts/free-port.mjs";

// Node's global, declared here because this project does not install @types/node.
declare const process: { env: Record<string, string | undefined> };

// Tauri needs the dev server on the same port as its devUrl. scripts/dev.mjs
// picks a free port and sets TPT_DEV_PORT; plain `npm run dev` picks one itself.
export default defineConfig(async ({ command }) => {
  const fromEnv = Number(process.env.TPT_DEV_PORT);
  const port = command === "serve" ? (fromEnv || (await findFreePort())) : undefined;

  return {
    clearScreen: false,
    server: port ? { port, strictPort: true } : undefined,
    build: { target: "es2021", outDir: "dist", emptyOutDir: true },
  };
});
