# eko

**eko** is a lightweight, local Burp Suite–style web proxy for inspecting and tinkering with HTTP(S) traffic. It’s a single Go binary with an embedded Svelte UI, SQLite-backed history, and a simple on/off app workflow — built to stay small and fast on low-memory machines.

> eko is open source for anyone to use and learn from. See [LICENSE](LICENSE).

## What it does

- **Proxy / History**: capture HTTP and HTTPS traffic (CONNECT + local CA TLS interception), browse a virtualized history with search and method/status filters, inspect headers and bodies (with gzip/deflate/brotli decoding and pretty JSON).
- **Projects**: keep separate workspaces (each with its own captured history/storage). Switch between projects or create new ones from the UI.
- **Scope**: define per-project include/exclude URL/host patterns (`*`, `?`) and filter history to “in scope” traffic.
- **Intercept**: pause matching requests/responses, edit raw bytes, then forward or drop (with a safety auto-forward timeout).
- **Repeater**: resend and tweak raw requests in tabs with resizable request/response panes; repeater traffic is recorded and filterable in history.
- **Intruder (basic)**: run simple payload attacks using `§§` markers in a request template with a payload list and results/response preview.
- **CA helpers**: download the local root CA and optionally import it into detected NSS databases (Firefox/Chromium profiles, `~/.pki/nssdb`) via `certutil`.
- **App-like controls**: runs on loopback only, per-run auth token for the control UI, one-click stop, and a launcher that can start/stop the daemon.

## How it fits together

```
Browser / FoxyProxy  ──HTTP proxy──▶  eko proxy (127.0.0.1:8080)
                                         │
                                         ├─ MITM + record/intercept/repeat
                                         ▼
                                   Control UI/API (127.0.0.1:8081)
                                   embedded Svelte app + SSE
                                         │
                                         ▼
                                   SQLite + blobs per project (~/.local/share/eko/projects/...)
```

## Requirements

- Linux (developed on Arch/Omarchy)
- Go 1.24+ to build the binary
- Node 20+ to build the UI from source (`make ui`)
- A Chromium-based browser for the app window (`chromium`, `chrome`, `brave`, etc.)
- `curl` for the launcher; `nss` (`certutil`) optional for one-click CA import

## Install / build

```sh
make            # build UI + binary into ./eko
make install    # install into ~/.local/bin, add app menu entry and icon
```

Make sure `~/.local/bin` is on your `PATH` (or run `~/.local/bin/eko-ui` / `~/.local/bin/eko` directly).

## Run (on / off)

Normal use is app-like — no terminal required:

- **On:** open **Eko** from the app menu (or run `eko-ui`). It starts the daemon if needed and opens the UI window.
- **Off:** click **⏻ stop** in the app header, choose **Stop Eko** from the app menu, or run `eko-ui stop`. Stopping turns the proxy off immediately.

CLI:

```sh
eko-ui             # start daemon if needed + open the app window
eko-ui status
eko-ui url
eko-ui stop

eko                # run daemon in the foreground
eko url            # print the tokenized UI URL
eko stop           # stop the daemon
eko ca             # print path to the root CA certificate
eko version
```

## Browser setup (FoxyProxy)

1. Add an HTTP proxy in FoxyProxy: host `127.0.0.1`, port `8080`.
2. Prefer **pattern-based** proxying (e.g. the targets you’re testing) and bypass `127.0.0.1` / `localhost` so the eko control UI isn’t proxied.
3. Browse — traffic appears in **History** for the active project.

### Trust the CA (HTTPS)

To inspect HTTPS, your browser/profile must trust eko’s local root CA:

- **Settings → HTTPS certificate** in the UI: download the CA and/or use **install into Firefox / Chromium (NSS)** (uses `certutil` on detected NSS DBs such as Firefox profiles and `~/.pki/nssdb`).
- CA file path: `~/.config/eko/ca.pem` (also `eko ca`).
- **Fully quit and reopen the browser** after importing trust (a reload is usually not enough).
- Keep `ca.pem` in place; use the **download root CA** button instead of moving the file. If the certificate is moved/deleted, eko refuses to start rather than silently mint a new CA (which would break existing browser trust). Delete both `ca.pem` and `ca-key.pem` to intentionally create a new CA.

Notes:
- Import the CA into the **same browser/profile** that FoxyProxy is using. Firefox uses its own per-profile certificate store.
- Certificate-pinned apps/sites will reject interception; add those hosts to **Passthrough hosts** in Settings to tunnel without MITM.

## Using eko

### Projects & scope
- Use the **project selector** in the header to switch projects or create a new one (each project has isolated history/storage).
- Right-click the project selector to **rename** or **delete** projects (switch away before deleting an active project).
- In **History**, use **scope…** to set include/exclude patterns for the active project and **in scope** to filter to matching traffic.

### History
Select a flow to inspect request/response headers and bodies (decoded for display, downloadable raw). Send a request to **Repeater** from the detail view.

### Intercept
Toggle **req** / **resp** to hold matching traffic. Edit raw request/response, then **Forward** (Ctrl+Enter) or **Drop**. Held items auto-forward after the configured timeout.

### Repeater
Create tabs, edit/paste a raw request (request line with an absolute URL), and **Send** (Ctrl+Enter). Request/response panes are resizable.

Example:

```http
GET https://target.example/api/me HTTP/1.1
Host: target.example
Accept: application/json

```

### Intruder (basic)
Use `§§` to mark the payload position in a request template, provide a payload list, choose concurrency, and run to view results and responses. (No scripting/match-replace/scanner yet.)

## Data & privacy

- Config: `~/.config/eko/settings.json`
- Root CA: `~/.config/eko/ca.{pem,key}`
- Data: `~/.local/share/eko/` (per-project stores under `projects/<id>/`, runtime/token info, eko Chromium profile)
- Proxy and control UI bind to **127.0.0.1** only; the API requires a per-run token (cookie or `X-Eko-Token`).

Clear captured data from the UI or remove project data as needed.

## Memory notes

Designed for modest machines: the daemon stays relatively small, while the browser UI window is the heavier part. Close the UI window when you don’t need it (the proxy can keep running depending on how it was started), keep max stored flows reasonable, and prefer pattern-based proxying to reduce noise and overhead.

## Development

```sh
make ui        # npm install (once) + vite build into internal/api/dist
make build     # go build -o eko
make vet
```

`internal/api/dist` must exist for `go:embed` builds. For UI work:

```sh
~/.local/bin/eko &            # daemon on :8080/:8081
cd web && npm run dev          # Vite dev server (proxy /api to the daemon)
```

Layout:

```
cmd/eko/            CLI, wiring, runtime, shutdown
internal/proxy/     HTTP proxy, CONNECT MITM, intercept, repeater
internal/flow/      model, SQLite store, body decode/raw parsing
internal/project/   projects, per-project stores, scope metadata
internal/ca/        root CA and per-host leaf certificates
internal/intercept/ pending queue and decisions
internal/api/       REST + SSE, auth, UI embed, NSS import
internal/glob/      wildcard matchers
internal/config/    settings
web/                Svelte 5 + Vite UI
packaging/          launcher, desktop entries, icon
```

## Limitations

- HTTP/1.1-focused proxying; no HTTP/2 or HTTP/3 MITM.
- WebSocket upgrades are passed/tunneled, not editable.
- Basic Intruder only (no scripting, match/replace rules, or scanner).
- Repeater doesn’t follow redirects or support WebSocket.
- Large bodies (above capture limits) may bypass full capture/intercept editing and stream through.
- Some responses may be opaque depending on encoding/transport settings (e.g. zstd handling).

## License

[MIT](LICENSE)
