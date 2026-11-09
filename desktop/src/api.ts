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

export async function startSession(input: StartSessionInput): Promise<void> {
	const response = await fetch(`${DAEMON_BASE}/sessions`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(input),
	});
	if (response.status === 409) throw new Error("Esa sesión ya existe.");
	if (!response.ok) throw new Error(`POST /sessions → ${response.status}`);
}

export function openLiveBoard(onError: () => void): EventSource {
	const stream = new EventSource(`${DAEMON_BASE}/board/live`);
	stream.addEventListener("error", onError);
	return stream;
}
