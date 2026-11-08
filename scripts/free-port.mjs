// Finds the first free TCP port at or above `start`. Shared by the Vite config
// and the dev launcher so both use the same port.
import net from "node:net";

export const DEV_PORT_START = 1421; // avoids 1420, which other local Tauri/Vite apps use
const MAX_TRIES = 50;

function isFree(port) {
  return new Promise((resolve) => {
    const server = net.createServer();
    server.once("error", () => resolve(false));
    server.once("listening", () => server.close(() => resolve(true)));
    server.listen(port, "127.0.0.1");
  });
}

export async function findFreePort(start = DEV_PORT_START) {
  for (let port = start; port < start + MAX_TRIES; port++) {
    if (await isFree(port)) return port;
  }
  throw new Error(`No free port found in ${start}-${start + MAX_TRIES - 1}`);
}
