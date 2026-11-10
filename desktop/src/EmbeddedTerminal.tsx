import { useEffect, useRef, useState } from "react";
import { DAEMON_BASE } from "./api";

/**
 * EmbeddedTerminal attaches to the session's PTY-backed websocket and renders
 * it in an xterm.js viewport. The daemon owns the shell; closing the drawer
 * only detaches — the terminal keeps its state server-side.
 */
export function EmbeddedTerminal({ sessionId }: { sessionId: string }) {
	const hostRef = useRef<HTMLDivElement>(null);
	const [status, setStatus] = useState<"connecting" | "live" | "closed">("connecting");
	const [unavailable, setUnavailable] = useState<string | null>(null);

	useEffect(() => {
		let disposed = false;
		let term: import("@xterm/xterm").Terminal | undefined;
		let socket: WebSocket | undefined;

		const setup = async () => {
			const [{ Terminal }, { FitAddon }] = await Promise.all([import("@xterm/xterm"), import("@xterm/addon-fit")]);
			if (disposed || !hostRef.current) return;

			term = new Terminal({
				fontSize: 11.5,
				fontFamily: "'IBM Plex Mono', ui-monospace, monospace",
				theme: {
					background: "#12080f",
					foreground: "#f2e8ee",
					cursor: "#f095c8",
					selectionBackground: "rgba(240,149,200,0.3)",
					black: "#12080f",
					brightBlack: "#6d5a66",
					green: "#7ee2b8",
					red: "#f0a9a9",
					yellow: "#f5a742",
					blue: "#8ab8f0",
					magenta: "#f095c8",
				},
				allowProposedApi: true,
			});
			const fit = new FitAddon();
			term.loadAddon(fit);
			term.open(hostRef.current);
			try {
				fit.fit();
			} catch {
				/* zero-size parent before layout; the next fit covers it */
			}

			const protocol = location.protocol === "https:" ? "wss" : "ws";
			socket = new WebSocket(`${protocol}${DAEMON_BASE.replace(/^http/, "")}/sessions/${encodeURIComponent(sessionId)}/terminal`);
			socket.binaryType = "arraybuffer";
			socket.onopen = () => {
				if (!disposed) setStatus("live");
			};
			socket.onmessage = (event) => {
				term?.write(new Uint8Array(event.data as ArrayBuffer));
			};
			socket.onclose = (event) => {
				if (disposed) return;
				setStatus("closed");
				if (event.reason) setUnavailable(event.reason);
			};
			socket.onerror = () => {
				if (!disposed) setStatus("closed");
			};
			socket.onopen;
			term.onData((data) => {
				if (socket?.readyState === WebSocket.OPEN) socket.send(new TextEncoder().encode(data));
			});

			const resize = () => {
				try {
					fit.fit();
				} catch {
					/* ignore */
				}
			};
			window.addEventListener("resize", resize);
			return () => window.removeEventListener("resize", resize);
		};

		let teardownResize: (() => void) | undefined;
		setup().then((cleanup) => {
			teardownResize = cleanup;
		});

		return () => {
			disposed = true;
			teardownResize?.();
			socket?.close();
			term?.dispose();
		};
	}, [sessionId]);

	if (unavailable) {
		return <p className="form-hint">{unavailable}</p>;
	}

	return (
		<div className={`term-wrap ${status}`}>
			<div ref={hostRef} className="term-host" />
			{status !== "live" && (
				<span className="term-badge">{status === "connecting" ? "conectando…" : "terminal cerrada"}</span>
			)}
		</div>
	);
}
