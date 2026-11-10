// Daemon API client. The desktop shell expects the Go daemon (orchestratord)
// on the local machine; the address is overridable for dev runs.

export const DAEMON_BASE: string =
	import.meta.env.VITE_ORCH_BASE ?? "http://127.0.0.1:8094";

export type Lane = "working" | "needs-you" | "in-review" | "ready-to-merge";

export const LANES: readonly Lane[] = ["working", "needs-you", "in-review", "ready-to-merge"];

export interface SessionCard {
	id: string;
	task: string;
	agent: string;
	branch: string;
	project?: string;
	gatePending?: boolean;
	pr?: number;
	running?: boolean;
	status?: string;
	question?: string;
}

export type Board = Record<Lane, SessionCard[] | null>;

export async function fetchBoard(): Promise<Board> {
	const response = await fetch(`${DAEMON_BASE}/board`);
	if (!response.ok) throw new Error(`GET /board → ${response.status}`);
	return (await response.json()) as Board;
}

export interface StartSessionInput {
	id: string;
	task: string;
	agent: string;
	branch: string;
}

export async function startSession(input: StartSessionInput & { launch?: boolean }): Promise<void> {
	const response = await fetch(`${DAEMON_BASE}/sessions`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(input),
	});
	if (response.status === 409) throw new Error("Esa sesión ya existe.");
	if (response.status === 400) {
		const text = await response.text();
		throw new Error(text.replace(/\n$/, ""));
	}
	if (!response.ok) throw new Error(`POST /sessions → ${response.status}`);
}

export function openLiveBoard(onError: () => void): EventSource {
	const stream = new EventSource(`${DAEMON_BASE}/board/live`);
	stream.addEventListener("error", onError);
	return stream;
}

export async function fetchAgents(): Promise<DetectedAgent[]> {
	const response = await fetch(`${DAEMON_BASE}/agents`);
	if (!response.ok) throw new Error(`GET /agents → ${response.status}`);
	return (await response.json()) as DetectedAgent[];
}

export interface DetectedAgent {
	agent: string;
	command: string;
	displayName: string;
	path?: string;
	version?: string;
	found: boolean;
}
export interface JournalEntry {
	at: string;
	kind: "launched" | "output" | "exited" | "stopped";
	text: string;
}

export async function fetchSessionEvents(id: string): Promise<JournalEntry[]> {
	const response = await fetch(`${DAEMON_BASE}/sessions/${encodeURIComponent(id)}/events`);
	if (!response.ok) throw new Error(`GET events → ${response.status}`);
	return (await response.json()) as JournalEntry[];
}

export async function fetchDiff(id: string): Promise<DiffInfo> {
	const response = await fetch(`${DAEMON_BASE}/sessions/${encodeURIComponent(id)}/diff`);
	if (response.status === 404) throw new Error("Sesión desconocida.");
	if (!response.ok) throw new Error((await response.text()).replace(/\n$/, ""));
	return (await response.json()) as DiffInfo;
}

export interface DiffFile {
	path: string;
	additions: number;
	deletions: number;
}

export interface DiffInfo {
	files: DiffFile[];
	patch: string;
}

export interface Evidence {
	sessionId: string;
	task: string;
	agent: string;
	branch: string;
	worktree?: string;
	status: string;
	summary?: string;
	decisions?: string[];
	receipt?: string;
	valid?: boolean;
	commits?: string[];
	startedAt?: string;
	updatedAt?: string;
	files?: DiffFile[];
	totalAdditions?: number;
	totalDeletions?: number;
	journal: JournalEntry[];
}

export async function fetchEvidence(id: string): Promise<Evidence> {
	const response = await fetch(`${DAEMON_BASE}/sessions/${encodeURIComponent(id)}/evidence`);
	if (response.status === 404) throw new Error("Sesión desconocida.");
	if (!response.ok) throw new Error(`GET evidence → ${response.status}`);
	return (await response.json()) as Evidence;
}
export interface AppConfig {
	repo: string;
	repoPath: string;
	listen?: string;
}

export async function saveConfig(config: AppConfig): Promise<string> {
	const response = await fetch(`${DAEMON_BASE}/config`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(config),
	});
	if (!response.ok) throw new Error((await response.text()).replace(/\n$/, ""));
	const body = (await response.json()) as { status?: string; note?: string };
	return body.note ?? "Guardado.";
}

export interface Project {
	id: string;
	repo: string;
	repoPath?: string;
}

export async function fetchProjects(): Promise<Project[]> {
	const response = await fetch(`${DAEMON_BASE}/projects`);
	if (!response.ok) return [];
	return (await response.json()) as Project[];
}

export const APP_VERSION = "0.1.0-alpha.1";
export const UPDATE_REPO = import.meta.env.VITE_UPDATE_REPO ?? "decode2/gentle-ai";

export interface LatestRelease {
	tag: string;
	url: string;
}

export async function fetchLatestRelease(): Promise<LatestRelease | null> {
	try {
		const response = await fetch(`https://api.github.com/repos/${UPDATE_REPO}/releases/latest`);
		if (!response.ok) return null;
		const body = (await response.json()) as { tag_name?: string; html_url?: string };
		if (!body.tag_name) return null;
		return { tag: body.tag_name, url: body.html_url ?? "" };
	} catch {
		return null;
	}
}

export async function fetchConfig(): Promise<AppConfig> {
	const response = await fetch(`${DAEMON_BASE}/config`);
	if (!response.ok) throw new Error(`GET /config → ${response.status}`);
	return (await response.json()) as AppConfig;
}

export async function openTerminal(id: string): Promise<void> {
	const response = await fetch(`${DAEMON_BASE}/sessions/${encodeURIComponent(id)}/terminal`, { method: "POST" });
	if (response.status === 404) throw new Error("Sesión desconocida.");
	if (!response.ok) throw new Error((await response.text()).replace(/\n$/, ""));
}
export async function stopSession(id: string): Promise<void> {
	const response = await fetch(`${DAEMON_BASE}/sessions/${encodeURIComponent(id)}/stop`, { method: "POST" });
	if (response.status === 404) throw new Error("No hay worker corriendo para esa sesión.");
	if (!response.ok) throw new Error(`POST stop → ${response.status}`);
}
