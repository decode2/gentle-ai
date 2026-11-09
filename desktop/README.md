# Gentle Orchestrator — desktop shell

La app de escritorio del orquestador: una ventana Tauri 2 + React que muestra
la flota de agentes y lanza el daemon Go (`orchestratord`) como sidecar.

## Arquitectura

```
desktop/
  src/            UI React (board SSE, form de sesión)
  src-tauri/      Shell Rust: spawnea el sidecar junto al binario de la app
  scripts/        sync-sidecar.sh: compila el daemon y lo deja como sidecar
```

El daemon es el producto: `GET /board`, `GET /board/live` (SSE) y
`POST /sessions` sobre `127.0.0.1:8094`. La UI consume eso directamente; el
shell solo abre la ventana y garantiza que el daemon viva mientras la app.

## Desarrollo

```bash
# 1. Daemon en otro terminal (o deja que la app lo spawnee):
go run ../../cmd/orchestratord --repo OWNER/REPO

# 2. UI con recarga:
pnpm install
pnpm dev            # vite en :5173; el shell Tauri la carga vía devUrl

# 3. Shell:
cargo tauri dev     # requiere tauri-cli (cargo install tauri-cli)
```

Para apuntar la UI a otro daemon: `VITE_ORCH_BASE=http://127.0.0.1:PUERTO`.

## Build de la app

```bash
../../scripts/... (desde la raíz del repo)
bash scripts/sync-sidecar.sh      # compila orchestratord al lado esperado
pnpm build                        # UI a dist/
cargo tauri build                 # bundle por OS (app/deb/rpm; dmg/msi en sus OS)
```

El sidecar se declara en `src-tauri/tauri.conf.json` (`externalBin`) y el
shell lo busca junto a su propio binario al arrancar; si no está, la UI
muestra el tablero como desconectado en lugar de fallar.

## Seguridad

- CORS con allow-list en el daemon: solo los orígenes del shell Tauri y el
  dev server de vite pueden llamarlo; un sitio arbitrario no puede crear
  sesiones en el daemon local.
- El shell no expone comandos al webview; toda la lógica vive en el daemon.
