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

export async function stopSession(id: string): Promise<void> {
	const response = await fetch(`${DAEMON_BASE}/sessions/${encodeURIComponent(id)}/stop`, { method: "POST" });
	if (response.status === 404) throw new Error("No hay worker corriendo para esa sesión.");
	if (!response.ok) throw new Error(`POST stop → ${response.status}`);
}
