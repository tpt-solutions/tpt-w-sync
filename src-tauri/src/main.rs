// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

//! tpt-sync tray and status app. It reads job definitions from the config
//! directory, asks the running daemon for live state, and sends run requests
//! over the same control channel the CLI uses.

mod ipc;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashMap;
use std::path::Path;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Manager, WindowEvent};

const NOT_RUNNING: &str = "daemon is not running";

/// The fields of a job definition the app displays. Unknown fields are ignored.
#[derive(Deserialize, Default)]
struct StoredJob {
    name: String,
    #[serde(default)]
    enabled: bool,
    #[serde(default)]
    source: String,
    #[serde(default)]
    destination: StoredDestination,
    #[serde(default)]
    schedule: String,
    #[serde(default)]
    watch: bool,
}

#[derive(Deserialize, Default)]
struct StoredDestination {
    #[serde(rename = "type", default)]
    kind: String,
    #[serde(default)]
    path: String,
    #[serde(default)]
    url: String,
    #[serde(default)]
    bucket: String,
}

#[derive(Deserialize)]
struct StatusItem {
    name: String,
    #[serde(default)]
    running: bool,
    #[serde(default)]
    last: Option<Value>,
}

#[derive(Serialize)]
struct JobView {
    name: String,
    enabled: bool,
    source: String,
    destination: String,
    schedule: String,
    watch: bool,
    running: bool,
    last: Option<Value>,
}

#[derive(Serialize)]
struct Overview {
    config_dir: String,
    daemon_running: bool,
    jobs: Vec<JobView>,
}

fn destination_label(d: &StoredDestination) -> String {
    match d.kind.as_str() {
        "s3" => format!("s3://{}/{}", d.bucket, d.path.trim_matches('/')),
        "webdav" => {
            let folder = d.path.trim_matches('/');
            if folder.is_empty() {
                d.url.clone()
            } else {
                format!("{}/{}", d.url.trim_end_matches('/'), folder)
            }
        }
        _ => d.path.clone(),
    }
}

/// Reads job definitions. A missing file means no jobs yet.
fn load_jobs(config_dir: &Path) -> Result<Vec<StoredJob>, String> {
    let path = config_dir.join("jobs.json");
    match std::fs::read_to_string(&path) {
        Ok(text) => {
            let mut jobs: Vec<StoredJob> = serde_json::from_str(&text)
                .map_err(|e| format!("reading {}: {e}", path.display()))?;
            jobs.sort_by(|a, b| a.name.cmp(&b.name));
            Ok(jobs)
        }
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(Vec::new()),
        Err(e) => Err(format!("reading {}: {e}", path.display())),
    }
}

/// Live state from the daemon, keyed by job name. Empty when it is not running.
fn live_statuses(config_dir: &Path) -> (bool, HashMap<String, StatusItem>) {
    match ipc::call(config_dir, "status", None) {
        Ok(resp) if resp.ok => {
            let items: Vec<StatusItem> = resp
                .data
                .and_then(|d| serde_json::from_value(d).ok())
                .unwrap_or_default();
            (
                true,
                items.into_iter().map(|s| (s.name.clone(), s)).collect(),
            )
        }
        _ => (false, HashMap::new()),
    }
}

/// Everything the window shows: jobs from disk merged with live daemon state.
#[tauri::command]
fn get_overview() -> Result<Overview, String> {
    let dir = ipc::config_dir()?;
    let jobs = load_jobs(&dir)?;
    let (daemon_running, live) = live_statuses(&dir);
    let views = jobs
        .into_iter()
        .map(|j| {
            let state = live.get(&j.name);
            JobView {
                running: state.map(|s| s.running).unwrap_or(false),
                last: state.and_then(|s| s.last.clone()),
                destination: destination_label(&j.destination),
                name: j.name,
                enabled: j.enabled,
                source: j.source,
                schedule: j.schedule,
                watch: j.watch,
            }
        })
        .collect();
    Ok(Overview {
        config_dir: dir.display().to_string(),
        daemon_running,
        jobs: views,
    })
}

/// Asks the daemon to run a job now and waits for the outcome. Runs on a
/// blocking thread so the window stays responsive during a long backup.
#[tauri::command]
async fn run_job(name: String) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let dir = ipc::config_dir()?;
        let resp = ipc::call(&dir, "run", Some(&name))?;
        if resp.ok {
            Ok(resp.data.unwrap_or(Value::Null))
        } else {
            Err(resp.error.unwrap_or_else(|| "run failed".to_string()))
        }
    })
    .await
    .map_err(|e| e.to_string())?
    .map_err(|e| {
        if e == NOT_RUNNING {
            "The tpt-sync daemon is not running. Start it with: tpt-sync daemon".to_string()
        } else {
            e
        }
    })
}

fn show_main_window(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    }
}

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![get_overview, run_job])
        .setup(|app| {
            let show = MenuItem::with_id(app, "show", "Show tpt-sync", true, None::<&str>)?;
            let quit = MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show, &quit])?;
            let icon = app
                .default_window_icon()
                .cloned()
                .ok_or("missing application icon")?;
            TrayIconBuilder::new()
                .icon(icon)
                .tooltip("tpt-sync")
                .menu(&menu)
                .show_menu_on_left_click(false)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "show" => show_main_window(app),
                    "quit" => app.exit(0),
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        show_main_window(tray.app_handle());
                    }
                })
                .build(app)?;
            Ok(())
        })
        // Closing the window keeps the app in the tray; "Quit" exits.
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tpt-sync");
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn destination_labels() {
        let s3 = StoredDestination {
            kind: "s3".into(),
            path: "/pc1/".into(),
            bucket: "b".into(),
            ..Default::default()
        };
        assert_eq!(destination_label(&s3), "s3://b/pc1");
        let dav = StoredDestination {
            kind: "webdav".into(),
            url: "https://dav.example.com/x/".into(),
            path: "".into(),
            ..Default::default()
        };
        assert_eq!(destination_label(&dav), "https://dav.example.com/x/");
        let local = StoredDestination {
            kind: "local".into(),
            path: r"D:\backup".into(),
            ..Default::default()
        };
        assert_eq!(destination_label(&local), r"D:\backup");
    }

    #[test]
    fn missing_jobs_file_is_empty_not_error() {
        let dir = std::env::temp_dir().join("tpt-sync-gui-no-such-dir");
        assert!(load_jobs(&dir).unwrap().is_empty());
    }

    #[test]
    fn reads_the_go_job_format() {
        let dir = std::env::temp_dir().join(format!("tpt-sync-gui-test-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::write(
            dir.join("jobs.json"),
            r#"[{"name":"docs","source":"/src","destination":{"type":"local","path":"/dst"},
                 "enabled":true,"schedule":"@daily","versioning":{"max_versions":3}}]"#,
        )
        .unwrap();
        let jobs = load_jobs(&dir).unwrap();
        assert_eq!(jobs.len(), 1);
        assert_eq!(jobs[0].name, "docs");
        assert!(jobs[0].enabled);
        assert_eq!(jobs[0].schedule, "@daily");
        std::fs::remove_dir_all(&dir).ok();
    }
}
