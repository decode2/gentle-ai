import { useCallback, useEffect, useRef, useState } from "react";
import {
	APP_VERSION,
	DAEMON_BASE,
	fetchAgents,
	fetchBoard,
	fetchDiff,
	fetchEvidence,
	fetchLatestRelease,
	fetchProjects,
	fetchSessionEvents,
	openLiveBoard,
	openTerminal,
	saveConfig,
	sendChat,
	startSession,
	stopSession,
	type Board,
	type DetectedAgent,
	type DiffInfo,
	type Evidence,
	type JournalEntry,
	type Lane,
	type Project,
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

const AGENT_DISPLAY_NAMES: Record<string, string> = {
	pi: "Gentle Shell",
	claude: "Claude Code",
	codex: "Codex",
	gemini: "Gemini CLI",
	opencode: "OpenCode",
	aider: "Aider",
	cursor: "Cursor",
	qwen: "Qwen Code",
	kimi: "Kimi CLI",
	crush: "Crush",
	amp: "Amp",
	goose: "Goose",
};

function agentColor(agent: string): string {
	return AGENT_COLORS[agent.toLowerCase()] ?? "#d7a0b8";
}

function agentDisplayName(agent: string, fallback?: string): string {
	return fallback ?? AGENT_DISPLAY_NAMES[agent.toLowerCase()] ?? agent;
}

function agentMonogram(agent: string, fallback?: string): string {
	const name = agentDisplayName(agent, fallback);
	const words = name.split(/\s+/).filter(Boolean);
	if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase();
	return name.slice(0, 2).toUpperCase();
}

export default function App() {
	const [board, setBoard] = useState<Board | null>(null);
	const [connected, setConnected] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [selected, setSelected] = useState<SessionCard | null>(null);
	const [agents, setAgents] = useState<DetectedAgent[]>([]);
	const [projects, setProjects] = useState<Project[]>([]);
	const [selectedProject, setSelectedProject] = useState<string>("");
	const [update, setUpdate] = useState<{ tag: string; url: string } | null>(null);
	const [toast, setToast] = useState<string | null>(null);
	const [theme, setTheme] = useState<"midnight" | "graphite" | "paper">(
		() => (localStorage.getItem("orch-theme") as "midnight" | "graphite" | "paper" | null) ?? "midnight",
	);
	const seenNeedsYou = useRef<Set<string>>(new Set());

	useEffect(() => {
		document.documentElement.dataset.theme = theme;
		localStorage.setItem("orch-theme", theme);
	}, [theme]);

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
		fetchProjects()
			.then(setProjects)
			.catch(() => setProjects([]));
		fetchLatestRelease().then((latest) => {
			// GitHub tags carry the v prefix; the running version doesn't.
			const normalized = latest?.tag.replace(/^v/, "") ?? "";
			if (latest && normalized !== APP_VERSION) setUpdate(latest);
		});
	}, []);

	const liveBoard = board === null ? null : selectedProject ? boardFor(board, selectedProject) : board;
	const total = liveBoard
		? LANES.reduce((sum, lane) => sum + (liveBoard[lane]?.length ?? 0), 0)
		: 0;
	const attention = liveBoard?.["needs-you"]?.length ?? 0;
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
					<button
						className="theme-btn"
						onClick={() =>
							setTheme((current) => (current === "midnight" ? "graphite" : current === "graphite" ? "paper" : "midnight"))
						}
						title="Cambiar tema"
					>
						{theme === "midnight" ? "🌙" : theme === "graphite" ? "◐" : "☀"}
					</button>
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

			{update && (
				<a className="update-banner" href={update.url} target="_blank" rel="noreferrer">
					Nueva versión: {update.tag} (corre {APP_VERSION})
				</a>
			)}

			<aside className="sidebar" aria-label="Sesiones y proyectos">
				{projects.length > 0 && (
					<nav aria-label="Proyectos">
						<h5>Proyectos</h5>
						<button
							className={`side-item ${selectedProject === "" ? "picked" : ""}`}
							onClick={() => setSelectedProject("")}
						>
							Todos los proyectos
						</button>
						{projects.map((p) => (
							<button
								key={p.id}
								className={`side-item ${selectedProject === p.id ? "picked" : ""}`}
								onClick={() => setSelectedProject(p.id)}
								title={p.repo}
							>
								{p.id}
							</button>
						))}
					</nav>
				)}
				<nav aria-label="Sesiones">
					<h5>Sesiones</h5>
					{liveBoard !== null && LANES.map((lane: Lane) =>
						(liveBoard[lane] ?? []).map((card: SessionCard) => (
							<button
								key={card.id}
								className={`side-item session-item ${selected?.id === card.id ? "picked" : ""}`}
								onClick={() => setSelected(card)}
								title={card.task}
							>
								<span className={`side-dot dot-${lane}`} aria-hidden="true" />
								<span className="side-label">{card.id}</span>
								{card.pr ? <span className="side-pr">#{card.pr}</span> : null}
							</button>
						)),
					)}
					{total === 0 && <p className="side-empty">Sin sesiones todavía</p>}
				</nav>
			</aside>

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
							cards={liveBoard![lane] ?? []}
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

/** boardFor filters a board down to one project's cards. */
function boardFor(board: Board, project: string): Board {
	const filtered = {} as Board;
	for (const lane of Object.keys(board) as Lane[]) {
		filtered[lane] = (board[lane] ?? []).filter((card) => card.project === project);
	}
	return filtered;
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
							{agentMonogram(card.agent)}
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
	const [terminalError, setTerminalError] = useState<string | null>(null);
	const [evidence, setEvidence] = useState<Evidence | null>(null);
	const [tab, setTab] = useState<"journal" | "terminal" | "diff">("journal");
	const journalRef = useRef<HTMLDivElement>(null);
	const drawerRef = useRef<HTMLElement>(null);
	const openerRef = useRef<HTMLElement | null>(null);

	// Focus management: remember the opener, land on the dialog, keep Tab
	// inside while open, and hand focus back on close.
	useEffect(() => {
		openerRef.current = document.activeElement as HTMLElement | null;
		const dialog = drawerRef.current;
		dialog?.focus();
		return () => openerRef.current?.focus();
	}, []);

	const trapTab = (event: React.KeyboardEvent) => {
		if (event.key !== "Tab" || !drawerRef.current) return;
		const focusables = drawerRef.current.querySelectorAll<HTMLElement>(
			'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
		);
		if (focusables.length === 0) return;
		const first = focusables[0];
		const last = focusables[focusables.length - 1];
		if (event.shiftKey && document.activeElement === first) {
			event.preventDefault();
			last.focus();
		} else if (!event.shiftKey && document.activeElement === last) {
			event.preventDefault();
			first.focus();
		}
	};

	// Card hover actions can open the drawer directly on a tab.
	useEffect(() => {
		const onDrawerTab = (event: Event) => {
			const detail = (event as CustomEvent<string>).detail;
			if (detail === "terminal" || detail === "journal" || detail === "diff") setTab(detail);
		};
		document.addEventListener("drawer-tab", onDrawerTab);
		return () => document.removeEventListener("drawer-tab", onDrawerTab);
	}, []);

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

	const openOsTerminal = async () => {
		setTerminalError(null);
		try {
			await openTerminal(card.id);
		} catch (cause) {
			setTerminalError(cause instanceof Error ? cause.message : String(cause));
		}
	};

	const changed = diff?.files.length ?? 0;
	const totalAdded = evidence?.totalAdditions ?? 0;
	const totalDeleted = evidence?.totalDeletions ?? 0;

	return (
		<>
			<div className="backdrop" onClick={onClose} aria-hidden="true" />
			<aside className="drawer" role="dialog" aria-modal="true" ref={drawerRef} tabIndex={-1} onKeyDown={trapTab} aria-label={`Sesión ${card.id}`}>
				<header>
					<span className="avatar big" style={{ background: agentColor(card.agent) }} title={agentDisplayName(card.agent)} aria-hidden="true">
						{agentMonogram(card.agent)}
					</span>
					<div className="drawer-title">
						<h3>{card.id}</h3>
						<span className="agent-name">{agentDisplayName(card.agent)}</span>
					</div>
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
				<nav className="drawer-tabs" role="tablist" aria-label="Vistas de la sesión">
					<button role="tab" aria-selected={tab === "journal"} className={`drawer-tab ${tab === "journal" ? "picked" : ""}`} onClick={() => setTab("journal")}>
						Journal
					</button>
					<button role="tab" aria-selected={tab === "terminal"} className={`drawer-tab ${tab === "terminal" ? "picked" : ""}`} onClick={() => setTab("terminal")}>
						Terminal
					</button>
					<button role="tab" aria-selected={tab === "diff"} className={`drawer-tab ${tab === "diff" ? "picked" : ""}`} onClick={() => setTab("diff")} disabled={changed === 0}>
						Diff ({changed})
					</button>
					<button className="tool os-term" onClick={openOsTerminal} title="Abrir la terminal del sistema en el worktree">
						⟫ OS
					</button>
				</nav>
				{terminalError && (
					<p className="banner" role="alert">
						{terminalError}
					</p>
				)}
				{tab === "terminal" && <EmbeddedTerminal sessionId={card.id} />}
				{tab === "diff" && diff && (
					<div className="diffview">
						<div className="diffstat">
							{diff.files.map((f) => (
								<p key={f.path}>
									<code>{f.path}</code>
									<span className="adds">+{f.additions}</span> <span className="dels">−{f.deletions}</span>
								</p>
							))}
						</div>
						<pre className="patch">
							{diff.patch.split("\n").map((line, i) => (
								<span
									key={i}
									className={
										line.startsWith("+") && !line.startsWith("+++")
											? "line-add"
											: line.startsWith("-") && !line.startsWith("---")
												? "line-del"
												: line.startsWith("@@")
													? "line-hunk"
													: undefined
									}
								>
									{line}
									{"\n"}
								</span>
							))}
						</pre>
					</div>
				)}
				{tab === "journal" && (
					<>
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
						<ChatComposer sessionId={card.id} />
						{stopError && (
							<p className="banner" role="alert">
								{stopError}
							</p>
						)}
						<button className="danger" onClick={stop}>
							Detener worker
						</button>
					</>
				)}
				{evidence && (
					<p className="evidence-line">
						{evidence.status}
						{totalAdded || totalDeleted ? (
							<> · <span className="adds">+{totalAdded}</span> <span className="dels">−{totalDeleted}</span></>
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
			</aside>
		</>
	);
}
/** ChatComposer: one message to the running worker's stdin. */
function ChatComposer({ sessionId }: { sessionId: string }) {
	const [message, setMessage] = useState("");
	const [error, setError] = useState<string | null>(null);
	const [sent, setSent] = useState(false);

	const send = async (event: React.FormEvent) => {
		event.preventDefault();
		if (!message.trim()) return;
		setError(null);
		setSent(false);
		try {
			await sendChat(sessionId, message.trim());
			setMessage("");
			setSent(true);
			setTimeout(() => setSent(false), 1500);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : String(cause));
		}
	};

	return (
		<form className="chat-composer" onSubmit={send}>
			<input
				value={message}
				onChange={(e) => setMessage(e.target.value)}
				placeholder="Escribile al worker…"
				aria-label="Mensaje para el worker"
			/>
			<button type="submit" disabled={!message.trim()}>
				Enviar
			</button>
			{sent && <span className="chat-sent">enviado</span>}
			{error && <p className="banner" role="alert">{error}</p>}
		</form>
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
										{agentMonogram(a.agent, a.displayName)}
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
