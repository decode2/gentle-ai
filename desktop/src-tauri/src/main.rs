// The Gentle-AI Orchestrator desktop shell. It launches the Go daemon
// (orchestratord) placed next to this application's binary and opens the
// board window. The daemon is the whole product; the shell is a window.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::path::PathBuf;
use std::process::{Child, Command};
use std::sync::Mutex;
use tauri::Manager;

struct DaemonChild(Mutex<Option<Child>>);

/// Reads orchestrator.json next to the app binary: {"repo", "repoPath",
/// "listen"}. Missing file or fields simply contribute no arguments.
fn daemon_arguments(app_dir: &std::path::Path) -> Vec<String> {
	let mut args = Vec::new();
	let raw = match std::fs::read_to_string(app_dir.join("orchestrator.json")) {
		Ok(raw) => raw,
		Err(_) => return args,
	};
	let Ok(value) = serde_json::from_str::<serde_json::Value>(&raw) else {
		return args;
	};
	for (key, flag) in [("repo", "--repo"), ("repoPath", "--repo-path"), ("listen", "--listen")] {
		if let Some(text) = value.get(key).and_then(|v| v.as_str()) {
			args.push(flag.to_string());
			args.push(text.to_string());
		}
	}
	args
}

/// Resolves the daemon binary next to the running app executable. The Tauri
/// bundler places `externalBin` payloads beside the app binary on every
/// platform, with the target triple suffix stripped at install time.
fn daemon_path() -> Option<PathBuf> {
	let exe = std::env::current_exe().ok()?;
	let mut path = exe.parent()?.to_path_buf();
	path.push("orchestratord");
	#[cfg(windows)]
	{
		path.set_extension("exe");
	}
	if path.exists() {
		Some(path)
	} else {
		None
	}
}

fn main() {
	let daemon = DaemonChild(Mutex::new(None));
	let sidecar = daemon_path();

	tauri::Builder::default()
		.manage(daemon)
		.setup(move |app| {
			match sidecar {
				Some(path) => {
					let app_dir = path.parent().map(|p| p.to_path_buf());
					let args = app_dir.as_deref().map(daemon_arguments).unwrap_or_default();
					let mut command = Command::new(&path);
					command.args(&args);
					// The windowed shell has no console: keep the daemon's output
					// in a log file beside the app so failures are diagnosable.
					if let Some(dir) = &app_dir {
						if let Ok(log) = std::fs::OpenOptions::new().create(true).append(true).open(dir.join("orchestratord.log")) {
							let log_err = log.try_clone();
							command.stdout(log);
							if let Ok(log_err) = log_err {
								command.stderr(log_err);
							}
						}
					}
					match command.spawn() {
						Ok(child) => {
							app.state::<DaemonChild>().0.lock().unwrap().replace(child);
						}
						Err(error) => eprintln!("orchestrator: could not spawn {}: {error}", path.display()),
					}
				}
				None => eprintln!(
					"orchestrator: orchestratord not found next to the app binary; the board will show as disconnected"
				),
			}
			Ok(())
		})
		.on_window_event(|window, event| {
			if let tauri::WindowEvent::Destroyed = event {
				let state = window.state::<DaemonChild>();
				let mut guard = state.0.lock().unwrap();
				if let Some(mut child) = guard.take() {
					let _ = child.kill();
				}
				drop(guard);
			}
		})
		.run(tauri::generate_context!())
		.expect("error while running the Gentle Orchestrator shell");
}
