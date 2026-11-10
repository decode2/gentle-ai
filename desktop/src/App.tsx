import { useCallback, useEffect, useRef, useState } from "react";
import {
	DAEMON_BASE,
	fetchAgents,
	fetchBoard,
	fetchDiff,
	fetchEvidence,
	fetchSessionEvents,
	openLiveBoard,
	openTerminal,
	saveConfig,
	startSession,
	stopSession,
	type Board,
	type DetectedAgent,
	type DiffInfo,
	type Evidence,
	type JournalEntry,
	type Lane,
	type SessionCard,
} from "./api";
import { EmbeddedTerminal } from "./EmbeddedTerminal";

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
	pi: "#f095c8",
	claude: "#e8865e",
	codex: "#c9d1d9",
	gemini: "#8ab8f0",
	aider: "#7ee2b8",
	cursor: "#b8a8f0",
	opencode: "#f5d76e",
	qwen: "#7ee2b8",
	kimi: "#8ab8f0",
	crush: "#f5a742",
	amp: "#c9d1d9",
	goose: "#f095c8",
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
	const [agents, setAgents] = useState<DetectedAgent[]>([]);
	const [toast, setToast] = useState<string | null>(null);
	const seenNeedsYou = useRef<Set<string>>(new Set());

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
					const next = JSON.parse((event as MessageEvent<string>).data) as Board;
					setBoard(next);
					setConnected(true);
					// A worker that newly needs you gets one toast; dismiss it
					// by clicking (the lane itself stays the source of truth).
					for (const card of next["needs-you"] ?? []) {
						if (!seenNeedsYou.current.has(card.id)) {
							seenNeedsYou.current.add(card.id);
							setToast(`${card.id} te necesita${card.question ? `: ${card.question}` : ""}`);
						}
					}
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

	useEffect(() => {
		fetchAgents()
			.then((roster) => setAgents(roster))
			.catch(() => setAgents([]));
	}, []);

	const total = board
		? LANES.reduce((sum, lane) => sum + (board[lane]?.length ?? 0), 0)
		: 0;
	const attention = board?.["needs-you"]?.length ?? 0;
	const installed = agents.filter((a) => a.found).length;

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
					{attention > 0 && <span className="attention-pill">{attention} te necesita</span>}
					<span className={`conn ${connected ? "ok" : "down"}`}>
						<span className="conn-dot" aria-hidden="true" />
						{connected ? "conectado" : "desconectado"}
					</span>
					<span className="fleet-count">
						{total} {total === 1 ? "worker" : "workers"} · {installed} agentes
					</span>
				</div>
			</header>

			{error && (
				<p className="banner" role="alert">
					{error}
				</p>
			)}

			{!connected && board !== null && <SetupPanel />}

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

			{toast && (
				<button className="toast" onClick={() => setToast(null)}>
					{toast}
				</button>
			)}

			{selected && <SessionDrawer card={selected} onClose={() => setSelected(null)} />}
			<SessionForm agents={agents} />
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
	const [diff, setDiff] = useState<DiffInfo | null>(null);
	const [showPatch, setShowPatch] = useState(false);
	const [terminalError, setTerminalError] = useState<string | null>(null);
	const [evidence, setEvidence] = useState<Evidence | null>(null);
	const [showTerminal, setShowTerminal] = useState(false);
	const journalRef = useRef<HTMLDivElement>(null);

	useEffect(() => {
		let cancelled = false;
		const load = async () => {
			try {
				const fresh = await fetchSessionEvents(card.id);
				if (!cancelled) setEntries(fresh);
				const freshDiff = await fetchDiff(card.id);
				if (!cancelled) setDiff(freshDiff);
				const freshEvidence = await fetchEvidence(card.id);
				if (!cancelled) setEvidence(freshEvidence);
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

	const terminal = async () => {
		setTerminalError(null);
		try {
			await openTerminal(card.id);
		} catch (cause) {
			setTerminalError(cause instanceof Error ? cause.message : String(cause));
		}
	};

	const changed = diff?.files.length ?? 0;

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
				<div className="actions-row">
					<button className="tool" onClick={() => setShowTerminal((v) => !v)}>
						{showTerminal ? "Ocultar terminal" : "⌨ Terminal"}
					</button>
					<button className="tool" onClick={terminal}>
						⟫ Terminal del OS
					</button>
					<button className="tool" onClick={() => setShowPatch((v) => !v)} disabled={changed === 0}>
						{showPatch ? "Ocultar diff" : `Diff (${changed})`}
					</button>
				</div>
				{terminalError && <p className="banner" role="alert">{terminalError}</p>}
				{showTerminal && <EmbeddedTerminal sessionId={card.id} />}
			{evidence && (
					<p className="evidence-line">
						{evidence.status}
						{evidence.totalAdditions || evidence.totalDeletions ? (
							<> · <span className="adds">+{evidence.totalAdditions}</span> <span className="dels">−{evidence.totalDeletions}</span></>
						) : null}
						{evidence.commits && evidence.commits.length > 0 ? <> · {evidence.commits.length} commits</> : null}
						{evidence.receipt ? (
						<span
							className={`receipt ${evidence.valid === false ? "receipt-bad" : ""}`}
							title={evidence.valid === false ? "El paquete de evidencia fue modificado después de que el worker lo congeló" : "Recibo de evidencia congelado al salir el worker"}
						>
							{" · "}{evidence.valid === false ? "⚠ recibo alterado " : ""}{evidence.receipt.slice(6, 14)}
						</span>
					) : null}
					</p>
				)}
				{evidence?.summary && <p className="worker-summary">{evidence.summary}</p>}
				{showPatch && diff && (
					<div className="diffview">
						<div className="diffstat">
							{diff.files.map((f) => (
								<p key={f.path}>
									<code>{f.path}</code>
									<span className="adds">+{f.additions}</span> <span className="dels">−{f.deletions}</span>
								</p>
							))}
						</div>
						<pre className="patch">{diff.patch || "(sin cambios contra la base)"}</pre>
					</div>
				)}
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

function SessionForm({ agents }: { agents: DetectedAgent[] }) {
	const [open, setOpen] = useState(false);
	const [fields, setFields] = useState({ id: "", task: "", agent: "", branch: "" });
	const [feedback, setFeedback] = useState<string | null>(null);
	const firstField = useRef<HTMLInputElement>(null);

	const installed = agents.filter((a) => a.found);

	useEffect(() => {
		if (open) firstField.current?.focus();
	}, [open]);

	// Default the agent to the first detected CLI once the roster arrives.
	useEffect(() => {
		setFields((current) =>
			current.agent === "" && installed.length > 0
				? { ...current, agent: installed[0].agent }
				: current,
		);
	}, [agents]); // eslint-disable-line react-hooks/exhaustive-deps

	const submit = async (event: React.FormEvent) => {
		event.preventDefault();
		setFeedback(null);
		try {
			await startSession({ ...fields, launch: true });
			setFields({ id: "", task: "", agent: installed[0]?.agent ?? "", branch: "" });
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
					{installed.length > 0 ? (
						<div className="agent-picker">
							{installed.map((a) => (
								<button
									type="button"
									key={a.agent}
									className={`agent-option ${fields.agent === a.agent ? "picked" : ""}`}
									onClick={() => setFields({ ...fields, agent: a.agent })}
									title={a.version || a.path}
								>
									<span className="avatar" style={{ background: agentColor(a.agent) }} aria-hidden="true">
										{agentInitial(a.agent)}
									</span>
									{a.agent}
								</button>
							))}
						</div>
					) : (
						<input
							required
							value={fields.agent}
							onChange={(e) => setFields({ ...fields, agent: e.target.value })}
							placeholder="pi, claude, codex..."
						/>
					)}
				</label>
				{installed.length === 0 && (
					<p className="form-hint">
						No detectamos agentes instalados — escribí el comando del CLI igual.
					</p>
				)}
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
					<button type="submit" disabled={fields.agent === ""}>
						Lanzar
					</button>
					<button type="button" onClick={() => setOpen(false)}>
						Cancelar
					</button>
				</div>
			</form>
		</>
	);
}

/** SetupPanel: the first-run onboarding when the daemon cannot be reached. */
function SetupPanel() {
	const [fields, setFields] = useState({ repo: "", repoPath: "" });
	const [note, setNote] = useState<string | null>(null);
	const [error, setError] = useState<string | null>(null);

	const save = async (event: React.FormEvent) => {
		event.preventDefault();
		setError(null);
		setNote(null);
		try {
			setNote(await saveConfig(fields));
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : String(cause));
		}
	};

	return (
		<form className="setup-panel rise" onSubmit={save}>
			<h2>Configurá tu repositorio</h2>
			<p className="form-hint">
				El daemon corre, pero sin repo: el tablero funciona y podés lanzar workers si
				señalás dónde vive tu proyecto.
			</p>
			<label>
				Repositorio (OWNER/REPO)
				<input
					required
					value={fields.repo}
					onChange={(e) => setFields({ ...fields, repo: e.target.value })}
					placeholder="decode2/gentle-ai"
				/>
			</label>
			<label>
				Checkout local (opcional, habilita lanzar)
				<input
					value={fields.repoPath}
					onChange={(e) => setFields({ ...fields, repoPath: e.target.value })}
					placeholder="C:\dev\mi-proyecto"
				/>
			</label>
			{note && <p role="status" className="form-hint">{note}</p>}
			{error && (
				<p className="banner" role="alert">
					{error}
				</p>
			)}
			<button type="submit">Guardar y reiniciar</button>
		</form>
	);
}
