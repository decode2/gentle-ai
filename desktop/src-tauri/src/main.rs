// The Gentle-AI Orchestrator desktop shell. It launches the Go daemon
// (orchestratord) placed next to this application's binary and opens the
// board window. The daemon is the whole product; the shell is a window.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::path::PathBuf;
use std::process::{Child, Command};
use std::sync::Mutex;
use tauri::Manager;

struct DaemonChild(Mutex<Option<Child>>);

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
				Some(path) => match Command::new(&path).spawn() {
					Ok(child) => {
						app.state::<DaemonChild>().0.lock().unwrap().replace(child);
					}
					Err(error) => eprintln!("orchestrator: could not spawn {}: {error}", path.display()),
				},
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
