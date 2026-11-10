// The Gentle-AI Orchestrator desktop shell. It launches the Go daemon
// (orchestratord) placed next to this application's binary and opens the
// board window. The daemon is the whole product; the shell is a window.
//
// Process lifecycle: whatever starts with the app, dies with the app. On
// Windows the daemon is bound to a Job Object with KILL_ON_JOB_CLOSE (the
// daemon's workers inherit that job), and on every platform the shell kills
// the daemon when the last window closes or the app exits.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::path::PathBuf;
use std::process::{Child, Command};
use std::sync::Mutex;
use tauri::Manager;

#[cfg(windows)]
use std::os::windows::io::AsRawHandle;

#[cfg(windows)]
mod job {
	//! Windows: bind the daemon to a kill-on-close job so daemon + workers
	//! (which inherit the job) die even on a hard shell crash.
	use windows_sys::Win32::Foundation::HANDLE;
	use windows_sys::Win32::System::JobObjects::{
		AssignProcessToJobObject, CreateJobObjectW, JobObjectExtendedLimitInformation,
		SetInformationJobObject, JOBOBJECT_EXTENDED_LIMIT_INFORMATION,
		JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
	};

	pub struct KillOnCloseJob(HANDLE);

	impl KillOnCloseJob {
		pub fn new() -> Result<Self, ()> {
			unsafe {
				let handle = CreateJobObjectW(std::ptr::null(), std::ptr::null());
				if handle.is_null() {
					return Err(());
				}
				let mut info: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = std::mem::zeroed();
				info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
				let ok = SetInformationJobObject(
					handle,
					JobObjectExtendedLimitInformation,
					&info as *const _ as *const core::ffi::c_void,
					std::mem::size_of::<JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() as u32,
				);
				if ok == 0 {
					return Err(());
				}
				Ok(KillOnCloseJob(handle))
			}
		}

		pub fn assign(&self, child_handle: HANDLE) -> Result<(), ()> {
			unsafe {
				if AssignProcessToJobObject(self.0, child_handle) == 0 {
					Err(())
				} else {
					Ok(())
				}
			}
		}
	}
}

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

/// Kills the daemon child, if one is running.
fn kill_daemon(app: &tauri::AppHandle) {
	let state = app.state::<DaemonChild>();
	let mut guard = state.0.lock().unwrap();
	if let Some(mut child) = guard.take() {
		let _ = child.kill();
		let _ = child.wait();
	}
}

fn main() {
	let daemon = DaemonChild(Mutex::new(None));
	let sidecar = daemon_path();

	let builder = tauri::Builder::default()
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
						if let Ok(log) = std::fs::OpenOptions::new()
							.create(true)
							.append(true)
							.open(dir.join("orchestratord.log"))
						{
							if let Ok(log_err) = log.try_clone() {
								command.stdout(log);
								command.stderr(log_err);
							}
						}
					}
					match command.spawn() {
						Ok(child) => {
							#[cfg(windows)]
							{
								let job = job::KillOnCloseJob::new();
								if let Ok(job) = job {
									let handle = child.as_raw_handle();
									if job.assign(handle).is_err() {
										eprintln!("orchestrator: daemon not bound to the kill-on-close job");
									}
								} else {
									eprintln!("orchestrator: could not create the kill-on-close job");
								}
							}
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
		});

	let app = builder
		.build(tauri::generate_context!())
		.expect("error while building the Gentle Orchestrator shell");

	app.run(|app_handle, event| match event {
		tauri::RunEvent::Exit
		| tauri::RunEvent::ExitRequested { .. }
		| tauri::RunEvent::WindowEvent { event: tauri::WindowEvent::Destroyed, .. } => {
			kill_daemon(app_handle);
		}
		_ => {}
	});
}
