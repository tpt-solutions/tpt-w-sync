// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

import { invoke } from "@tauri-apps/api/core";

interface RunRecord {
  started: string;
  finished: string;
  snapshot?: string;
  ok: boolean;
  error?: string;
  copied: number;
  linked: number;
  deleted: number;
}

interface JobView {
  name: string;
  enabled: boolean;
  source: string;
  destination: string;
  schedule: string;
  watch: boolean;
  running: boolean;
  last: RunRecord | null;
}

interface Overview {
  config_dir: string;
  daemon_running: boolean;
  jobs: JobView[];
}

const REFRESH_MS = 5000;

const el = <T extends HTMLElement>(id: string): T => {
  const node = document.getElementById(id);
  if (!node) throw new Error(`missing element #${id}`);
  return node as T;
};

const jobsBody = el<HTMLTableSectionElement>("jobs");
const daemonPill = el<HTMLSpanElement>("daemon");
const notice = el<HTMLParagraphElement>("notice");
const empty = el<HTMLParagraphElement>("empty");
const configLine = el<HTMLParagraphElement>("config");

// Runs in progress from this window, so their buttons stay disabled across refreshes.
const busy = new Set<string>();

function showNotice(message: string | null): void {
  notice.textContent = message ?? "";
  notice.hidden = message === null;
}

function cell(text: string, className?: string): HTMLTableCellElement {
  const td = document.createElement("td");
  td.textContent = text;
  if (className) td.className = className;
  return td;
}

function formatTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

function lastRunText(last: RunRecord | null): { text: string; failed: boolean } {
  if (!last) return { text: "never", failed: false };
  if (!last.ok) {
    return { text: `failed ${formatTime(last.started)}: ${last.error ?? "unknown error"}`, failed: true };
  }
  return {
    text: `${formatTime(last.started)} — ${last.copied} copied, ${last.linked} linked`,
    failed: false,
  };
}

function renderRow(job: JobView): HTMLTableRowElement {
  const row = document.createElement("tr");
  row.dataset.job = job.name;

  const name = cell(job.name, "name");
  if (!job.enabled) {
    const off = document.createElement("span");
    off.className = "tag";
    off.textContent = "disabled";
    name.append(" ", off);
  }
  row.append(name);
  row.append(cell(job.destination, "mono"));
  row.append(cell(job.schedule || (job.watch ? "on change" : "manual")));

  const isRunning = job.running || busy.has(job.name);
  row.append(cell(isRunning ? "running…" : "idle", isRunning ? "state-running" : ""));

  const last = lastRunText(job.last);
  row.append(cell(last.text, last.failed ? "state-failed" : ""));

  const actions = document.createElement("td");
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = "Run now";
  button.disabled = isRunning || !daemonRunning;
  button.addEventListener("click", () => void runJob(job.name));
  actions.append(button);
  row.append(actions);
  return row;
}

let daemonRunning = false;

function render(overview: Overview): void {
  daemonRunning = overview.daemon_running;
  daemonPill.textContent = daemonRunning ? "daemon running" : "daemon not running";
  daemonPill.className = `pill ${daemonRunning ? "ok" : "warn"}`;
  showNotice(
    daemonRunning
      ? null
      : "The tpt-sync daemon is not running, so jobs cannot be started from here. Start it with: tpt-sync daemon",
  );
  configLine.textContent = `Config: ${overview.config_dir}`;

  jobsBody.replaceChildren(...overview.jobs.map(renderRow));
  empty.hidden = overview.jobs.length > 0;
}

async function refresh(): Promise<void> {
  try {
    render(await invoke<Overview>("get_overview"));
  } catch (err) {
    daemonPill.textContent = "error";
    daemonPill.className = "pill warn";
    showNotice(`Could not read job state: ${String(err)}`);
  }
}

async function runJob(name: string): Promise<void> {
  busy.add(name);
  await refresh();
  try {
    await invoke<RunRecord>("run_job", { name });
    showNotice(`Run of "${name}" finished.`);
  } catch (err) {
    showNotice(`Run of "${name}" failed: ${String(err)}`);
  } finally {
    busy.delete(name);
    await refresh();
  }
}

void refresh();
setInterval(() => void refresh(), REFRESH_MS);
