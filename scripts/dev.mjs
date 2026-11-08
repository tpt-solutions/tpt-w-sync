// Starts the Tauri desktop app in dev mode on a free port.
// Usage: npm run dev:app
// Picks the first free port from DEV_PORT_START, then passes it to both the
// Vite dev server (via TPT_DEV_PORT) and the Tauri window (devUrl override),
// so the window never loads some other app that already owns the port.
import { spawn } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { findFreePort } from "./free-port.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const port = await findFreePort();
const devUrl = `http://localhost:${port}`;

console.log(`tpt-sync dev server will use ${devUrl}`);

const tauriCli = path.join(root, "node_modules", "@tauri-apps", "cli", "tauri.js");
const child = spawn(
  process.execPath,
  [tauriCli, "dev", "--config", JSON.stringify({ build: { devUrl } })],
  {
    cwd: root,
    stdio: "inherit",
    env: { ...process.env, TPT_DEV_PORT: String(port) },
  },
);

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => child.kill(signal));
}
child.on("exit", (code) => process.exit(code ?? 0));
