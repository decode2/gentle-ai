import { useCallback, useEffect, useState } from "react";
import { DAEMON_BASE, LANES, fetchBoard, openLiveBoard, startSession, type Board, type SessionCard } from "./api";

const EMPTY_BOARD: Board = { working: null, "needs-you": null, "in-review": null, "ready-to-merge": null };

const LANE_LABELS: Record<string, string> = {
	"working": "Working",
	"needs-you": "Needs you",
	"in-review": "In review",
	"ready-to-merge": "Ready to merge",
};

export default function App() {
	const [board, setBoard] = useState<Board>(EMPTY_BOARD);
	const [connected, setConnected] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const markDisconnected = useCallback(() => setConnected(false), []);

	useEffect(() => {
		let stream: EventSource | undefined;
		let cancelled = false;

		fetchBoard()
			.then((fresh) => {
				if (!cancelled) {
					setBoard(fresh);
					setConnected(true);
				}
				stream = openLiveBoard(markDisconnected);
				stream.addEventListener("board", (event) => {
					setBoard(JSON.parse((event as MessageEvent<string>).data) as Board);
					setConnected(true);
				});
			})
			.catch(() => setError(`No se pudo conectar con el daemon en ${DAEMON_BASE}. ¿Está corriendo orchestratord?`));

		return () => {
			cancelled = true;
			stream?.close();
		};
	}, [markDisconnected]);

	return (
		<main className="shell">
			<header className="topbar">
				<h1>Gentle Orchestrator</h1>
				<span className={`status ${connected ? "ok" : "down"}`}>
					{connected ? "daemon conectado" : "daemon desconectado"}
				</span>
			</header>
			{error && <p className="banner" role="alert">{error}</p>}
			<section className="lanes">
				{LANES.map((lane) => (
					<LaneColumn key={lane} lane={lane} cards={board[lane] ?? []} />
				))}
			</section>
			<SessionForm />
		</main>
	);
}

function LaneColumn({ lane, cards }: { lane: string; cards: SessionCard[] }) {
	return (
		<div className={`lane lane-${lane}`}>
			<h2>
				{LANE_LABELS[lane] ?? lane} <span className="count">{cards.length}</span>
			</h2>
			{cards.length === 0 && <p className="empty">(empty)</p>}
			{cards.map((card) => (
				<article key={card.id} className="card">
					<header>
						<strong>{card.id}</strong>
						<span className="agent">{card.agent}</span>
					</header>
					<p className="task">{card.task}</p>
					<footer>
						<code>{card.branch}</code>
						{card.pr ? <span className="pr">#{card.pr}</span> : null}
					</footer>
				</article>
			))}
		</div>
	);
}

function SessionForm() {
	const [open, setOpen] = useState(false);
	const [fields, setFields] = useState({ id: "", task: "", agent: "pi", branch: "" });
	const [feedback, setFeedback] = useState<string | null>(null);

	const submit = async (event: React.FormEvent) => {
		event.preventDefault();
		setFeedback(null);
		try {
			await startSession(fields);
			setFeedback(`Sesión "${fields.id}" registrada.`);
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
		<form className="session-form" onSubmit={submit}>
			<h3>Nueva sesión</h3>
			<label>
				ID
				<input required value={fields.id} onChange={(e) => setFields({ ...fields, id: e.target.value })} placeholder="metering" />
			</label>
			<label>
				Tarea
				<input required value={fields.task} onChange={(e) => setFields({ ...fields, task: e.target.value })} placeholder="Meter API calls per workspace" />
			</label>
			<label>
				Agente
				<input required value={fields.agent} onChange={(e) => setFields({ ...fields, agent: e.target.value })} placeholder="pi" />
			</label>
			<label>
				Rama
				<input required value={fields.branch} onChange={(e) => setFields({ ...fields, branch: e.target.value })} placeholder="orch/metering-api" />
			</label>
			{feedback && <p role="status">{feedback}</p>}
			<div className="actions">
				<button type="submit">Registrar</button>
				<button type="button" onClick={() => setOpen(false)}>
					Cancelar
				</button>
			</div>
		</form>
	);
}
