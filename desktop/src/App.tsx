import { useCallback, useEffect, useRef, useState } from "react";
import {
	DAEMON_BASE,
	fetchBoard,
	fetchSessionEvents,
	openLiveBoard,
	startSession,
	stopSession,
	type Board,
	type JournalEntry,
	type Lane,
	type SessionCard,
} from "./api";

const EMPTY_BOARD: Board = { working: null, "needs-you": null, "in-review": null, "ready-to-merge": null };

/** Attention first; the rest flows toward merge. */
const LANES: readonly Lane[] = ["needs-you", "working", "in-review", "ready-to-merge"];

const LANE_LABELS: Record<Lane, string> = {
	"needs-you": "Te necesita",
	"working": "Trabajando",
	"in-review": "En revisión",
	"ready-to-merge": "Lista para mergear",
};

const LANE_EMPTY: Record<Lane, string> = {
	"needs-you": "Nada esperándote",
	"working": "Sin workers activos",
	"in-review": "Ningún PR abierto",
	"ready-to-merge": "Nada aprobado aún",
};

/** Brand-adjacent agent colors: the monogram disc encodes the agent. */
const AGENT_COLORS: Record<string, string> = {
	"pi": "#f095c8",
	"claude": "#e8865e",
	"codex": "#c9d1d9",
	"gemini": "#8ab8f0",
	"aider": "#7ee2b8",
	"cursor": "#b8a8f0",
	"opencode": "#f5d76e",
};

function agentColor(agent: string): string {
	return AGENT_COLORS[agent.toLowerCase()] ?? "#d7a0b8";
}

function agentInitial(agent: string): string {
	return agent.slice(0, 2).toUpperCase();
}

export default function App() {
	const [board, setBoard] = useState<Board | null>(null);
	const [connected, setConnected] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [selected, setSelected] = useState<SessionCard | null>(null);

	const markDisconnected = useCallback(() => setConnected(false), []);

	useEffect(() => {
		let stream: EventSource | undefined;
		let cancelled = false;

		fetchBoard()
			.then((fresh) => {
				if (cancelled) return;
				setBoard(fresh);
				setConnected(true);
				stream = openLiveBoard(markDisconnected);
				stream.addEventListener("board", (event) => {
					setBoard(JSON.parse((event as MessageEvent<string>).data) as Board);
					setConnected(true);
				});
			})
			.catch(() => {
				if (!cancelled) {
					setBoard(EMPTY_BOARD);
					setError(`No se pudo conectar con el daemon en ${DAEMON_BASE}. ¿Está corriendo orchestratord?`);
				}
			});

		return () => {
			cancelled = true;
			stream?.close();
		};
	}, [markDisconnected]);

	const total = board
		? LANES.reduce((sum, lane) => sum + (board[lane]?.length ?? 0), 0)
		: 0;

	return (
		<main className="shell">
			<div className="glow" aria-hidden="true" />
			<header className="topbar">
				<div className="wordmark">
					<span className="mark" aria-hidden="true">
						<svg viewBox="0 0 24 24" width="22" height="22">
							<circle cx="12" cy="12" r="10" fill="none" stroke="var(--rose)" strokeWidth="2.6" />
							<circle cx="12" cy="12" r="3.4" fill="var(--rose)" />
						</svg>
					</span>
					<span className="name">Gentle Orchestrator</span>
				</div>
				<div className="top-meta">
					<span className={`conn ${connected ? "ok" : "down"}`}>
						<span className="conn-dot" aria-hidden="true" />
						{connected ? "conectado" : "desconectado"}
					</span>
					<span className="fleet-count">{total} {total === 1 ? "worker" : "workers"}</span>
				</div>
			</header>

			{error && (
				<p className="banner" role="alert">
					{error}
				</p>
			)}

			{board === null ? (
				<section className="lanes" aria-busy="true">
					{LANES.map((lane) => (
						<div key={lane} className={`lane lane-${lane}`}>
							<h2>{LANE_LABELS[lane]}</h2>
							<div className="skeleton" />
							<div className="skeleton" />
						</div>
					))}
				</section>
			) : total === 0 ? (
				<section className="empty-board">
					<h2>Ningún worker todavía</h2>
					<p>
						Lanzá el primero con <strong>«+ Nueva sesión»</strong>: el daemon crea el worktree,
						corre el agente ahí y su PR entra al tablero.
					</p>
				</section>
			) : (
				<section className="lanes">
					{LANES.map((lane, index) => (
						<LaneColumn
							key={lane}
							lane={lane}
							cards={board[lane] ?? []}
							onSelect={setSelected}
							style={{ "--enter-delay": `${index * 45}ms` } as React.CSSProperties}
						/>
					))}
				</section>
			)}

			{selected && <SessionDrawer card={selected} onClose={() => setSelected(null)} />}
			<SessionForm />
		</main>
	);
}

function LaneColumn({
	lane,
	cards,
	onSelect,
	style,
}: {
	lane: Lane;
	cards: SessionCard[];
	onSelect: (card: SessionCard) => void;
	style?: React.CSSProperties;
}) {
	return (
		<div className={`lane lane-${lane} enter`} style={style}>
			<h2>
				<span className="lane-dot" aria-hidden="true" />
				{LANE_LABELS[lane]}
				<span className="count">{cards.length}</span>
			</h2>
			{cards.length === 0 && <p className="lane-empty">{LANE_EMPTY[lane]}</p>}
			{cards.map((card) => (
				<article
					key={card.id}
					className="card"
					onClick={() => onSelect(card)}
					role="button"
					tabIndex={0}
					onKeyDown={(e) => e.key === "Enter" && onSelect(card)}
				>
					<header>
						<span className="avatar" style={{ background: agentColor(card.agent) }} aria-hidden="true">
							{agentInitial(card.agent)}
						</span>
						<strong className="card-id">{card.id}</strong>
						{card.pr ? <span className="pr">#{card.pr}</span> : null}
						{card.running && <span className="pulse" title="corriendo" aria-label="corriendo" />}
					</header>
					<p className="task">{card.task}</p>
					{card.question ? (
						<p className="status status-question">{card.question}</p>
					) : (
						<p className={`status status-${lane}`}>{card.status}</p>
					)}
					<footer>
						<code>{card.branch}</code>
					</footer>
				</article>
			))}
		</div>
	);
}

function SessionDrawer({ card, onClose }: { card: SessionCard; onClose: () => void }) {
	const [entries, setEntries] = useState<JournalEntry[]>([]);
	const [stopError, setStopError] = useState<string | null>(null);
	const journalRef = useRef<HTMLDivElement>(null);

	useEffect(() => {
		let cancelled = false;
		const load = async () => {
			try {
				const fresh = await fetchSessionEvents(card.id);
				if (!cancelled) setEntries(fresh);
			} catch {
				/* the connection badge already shows daemon problems */
			}
		};
		load();
		const timer = setInterval(load, 2000);
		return () => {
			cancelled = true;
			clearInterval(timer);
		};
	}, [card.id]);

	useEffect(() => {
		const el = journalRef.current;
		if (el) el.scrollTop = el.scrollHeight;
	}, [entries]);

	useEffect(() => {
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [onClose]);

	const stop = async () => {
		setStopError(null);
		try {
			await stopSession(card.id);
		} catch (cause) {
			setStopError(cause instanceof Error ? cause.message : String(cause));
		}
	};

	return (
		<>
			<div className="backdrop" onClick={onClose} aria-hidden="true" />
			<aside className="drawer" role="dialog" aria-label={`Sesión ${card.id}`}>
				<header>
					<span className="avatar big" style={{ background: agentColor(card.agent) }} aria-hidden="true">
						{agentInitial(card.agent)}
					</span>
					<h3>{card.id}</h3>
					<button className="icon-btn" onClick={onClose} aria-label="Cerrar">
						✕
					</button>
				</header>
				<p className="task">{card.task}</p>
				<div className="chips">
					<code className="chip">{card.branch}</code>
					{card.pr ? <span className="chip chip-pr">#{card.pr}</span> : null}
					<span className={`chip ${card.running ? "chip-live" : ""}`}>
						{card.running ? "corriendo" : "detenido"}
					</span>
				</div>
				<h4>Journal</h4>
				<div className="journal" ref={journalRef}>
					{entries.length === 0 && <p className="lane-empty">(sin eventos)</p>}
					{entries.map((entry, index) => (
						<p key={index} className={`entry entry-${entry.kind}`}>
							<span className="entry-meta">
								<span className="kind">{entry.kind}</span>
								<time>{new Date(entry.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</time>
							</span>
							{entry.text}
						</p>
					))}
				</div>
				{stopError && (
					<p className="banner" role="alert">
						{stopError}
					</p>
				)}
				<button className="danger" onClick={stop}>
					Detener worker
				</button>
			</aside>
		</>
	);
}

function SessionForm() {
	const [open, setOpen] = useState(false);
	const [fields, setFields] = useState({ id: "", task: "", agent: "pi", branch: "" });
	const [feedback, setFeedback] = useState<string | null>(null);
	const firstField = useRef<HTMLInputElement>(null);

	useEffect(() => {
		if (open) firstField.current?.focus();
	}, [open]);

	const submit = async (event: React.FormEvent) => {
		event.preventDefault();
		setFeedback(null);
		try {
			await startSession({ ...fields, launch: true });
			setFields({ id: "", task: "", agent: "pi", branch: "" });
			setOpen(false);
		} catch (cause) {
			setFeedback(cause instanceof Error ? cause.message : String(cause));
		}
	};

	if (!open) {
		return (
			<button className="fab" onClick={() => setOpen(true)}>
				+ Nueva sesión
			</button>
		);
	}

	return (
		<>
			<div className="backdrop" onClick={() => setOpen(false)} aria-hidden="true" />
			<form className="session-form rise" onSubmit={submit}>
				<h3>Lanzar worker</h3>
				<p className="form-hint">El daemon crea el worktree y corre el agente ahí.</p>
				<label>
					ID
					<input
						ref={firstField}
						required
						value={fields.id}
						onChange={(e) => setFields({ ...fields, id: e.target.value })}
						placeholder="metering"
					/>
				</label>
				<label>
					Tarea
					<input
						required
						value={fields.task}
						onChange={(e) => setFields({ ...fields, task: e.target.value })}
						placeholder="Medir llamadas API por workspace"
					/>
				</label>
				<label>
					Agente
					<input
						required
						value={fields.agent}
						onChange={(e) => setFields({ ...fields, agent: e.target.value })}
						placeholder="pi"
					/>
				</label>
				<label>
					Rama
					<input
						required
						value={fields.branch}
						onChange={(e) => setFields({ ...fields, branch: e.target.value })}
						placeholder="orch/metering-api"
					/>
				</label>
				{feedback && (
					<p className="banner" role="alert">
						{feedback}
					</p>
				)}
				<div className="actions">
					<button type="submit">Lanzar</button>
					<button type="button" onClick={() => setOpen(false)}>
						Cancelar
					</button>
				</div>
			</form>
		</>
	);
}
