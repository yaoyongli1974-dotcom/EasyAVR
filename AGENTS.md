# AGENTS.md

EasyAVR is an **AI-native video fusion & intelligent video resource platform** (not an EasyCVR clone).
Three planes (device access / video / AI) + two centers (video resources / AI events) + an open API layer.
Read `docs/architecture.md` before changing module boundaries.

## Layout & where to work

- `server/` — Go module root (`github.com/easyavr/easyavr`). **Run all Go commands from `server/`, not repo root.**
  - `internal/device` device access plane; `internal/video` ZLMediaKit client; `internal/ai` provider abstraction + task runner; `internal/event` event center; `internal/server` Gin routes/handlers.
  - `internal/recording` recording control + playback catalog + schedule runner; `internal/snapshot` ffmpeg snapshot capture; `internal/gb28181` SIP signaling (REGISTER/keepalive/catalog/alarm/INVITE/cascade); `internal/media` shared ffmpeg frame grab.
  - `internal/search` semantic (embedding + cosine) + keyword retrieval; `internal/notify` webhook/email alert delivery; `internal/cluster` node registry/heartbeat + device assignment; `internal/ga1400` GA/T1400 (VIID) XML ingest + cascade; `internal/ehome` Hikvision ISUP intake endpoint (pure-Go UDP by default; optional native SDK via `-tags ehome_sdk`); `internal/gb35114` SM2/SM3 certificate authority (uses `github.com/tjfoc/gmsm`).
- `web/` — Vue 3 + Vite + TS (Element Plus, Pinia). Hash router. `/api` proxied to `:18000` in dev.
- `examples/cv-worker/` — stdlib-only reference CV worker proving the AI ingest contract.
- `docs/architecture.md` — the source of truth for the plane/center design.

## Commands (from repo root)

```bash
make deps        # go mod download + npm install
make run         # backend API on :18000 (auto-migrates + seeds admin; SQLite at server/data/)
make web-dev     # Vite dev server on :5173
make test        # Go tests
make vet         # go vet
make build       # server binary + web/dist
make up / down   # ZLMediaKit via docker compose
```

Single Go test: `cd server && go test ./internal/server -run TestPlatformLifecycle -v`.
Verification order when changing backend: `make test` then `make vet`. Frontend: `cd web && npm run build` (runs `vue-tsc` typecheck first).

## Non-obvious facts

- **Go toolchain**: `server/go.mod` declares `go 1.26.0`, but the system Go is older. Go's auto-toolchain downloads 1.26.0 on first build — this is expected, do not "fix" the go directive or set `GOTOOLCHAIN=local`.
- **ZLMediaKit is external**: the platform shells out to ZLM's HTTP API. `EASYAVR_ZLM_SECRET` must match ZLM's `config.ini`; `docker-compose.yml` pins a non-default secret via `deploy/zlm/config.ini` (ZLM regenerates the secret when it is the stock default, so don't restore `035c73f7-...`). Most video endpoints fail gracefully (502) when ZLM is down.
- **AI tasks need `ffmpeg` on PATH** to grab frames (`internal/media/frame.go`), and so do snapshots. The integration tests avoid this by setting `grabFrame:false` / not capturing, so `make test` needs **no ZLM and no ffmpeg**.
- **GB28181 is Home-grown Go SIP signaling** (`internal/gb28181`), not a Java/third-party stack. It is disabled unless `EASYAVR_GB_ENABLED=true`; when disabled `App.gb` is nil and GB routes still respond (`/gb/config` reports `enabled:false`). Its test drives the full REGISTER→keepalive→catalog flow over UDP loopback with no media.
- **Recording is delegated to ZLM** (`startRecord`/`getMp4RecordFile`); playback URLs point at ZLM's `/record/...` HTTP path. The platform only catalogs entries — it does not delete media files.
- Snapshot images are served unauthenticated at `/snapshots/*` so `<img>` tags work; all other routes require the JWT.
- **Newly created AI events fan out through one `eventSink`** (`internal/server/sink.go`) to semantic indexing and notifications. If you add a code path that creates `model.AIEvent`, call `a.sink.OnEvent(ev)` (after the DB create, so the ID is set) or GB/GA ingest won't notify/index.
- Semantic search silently falls back to keyword matching when no `kind=embedding` provider is enabled; GH/GA ingest endpoints (`/VIID/*`, `/api/v1/cluster/heartbeat`) and snapshots are mounted **outside** the JWT middleware.
- **AI providers**: `cv` posts to an external detector (contract in `internal/ai/httpdetector.go`); `vlm`/`llm` speak OpenAI-compatible `/v1/chat/completions`. API keys are never serialized (`json:"-"`).
- **Stream keys** are generated server-side (`ch_<16 hex>`); they are the ZLM `app/stream` id and are unique. Don't rename them without updating ZLM proxies.
- Backend is **pure Go** (no CGO): SQLite uses `glebarez/sqlite`, PostgreSQL uses `jackc/pgx`, and GB35114 uses `tjfoc/gmsm` for SM2/SM3. Adding a CGO dependency breaks the default build.
- The database is pluggable: `store.OpenWith(driver, dsn)` selects `sqlite` (default, `EASYAVR_DB`) or `postgres` (`EASYAVR_DB_DSN`) via `EASYAVR_DB_DRIVER`. SQLite runs with WAL + busy_timeout + foreign_keys.
- The backend serves `web/dist` when present (`EASYAVR_WEB_DIST`, default `../web/dist` relative to `server/`); `NoRoute` falls back to `index.html` for the SPA, except `/api/*`.
- UI copy and some comments are in Chinese; keep that consistent.

## Conventions

- API responses are always `{code, message, data}`; `code:0` means success (`ok`/`fail` helpers in `internal/server/app.go`).
- Auth is JWT Bearer; protected routes go through `authRequired()`.
- GORM models live in `internal/model/models.go`; migrations are automatic via `AutoMigrate`.
- Don't add comments to code unless they carry non-obvious intent (repo style keeps comments sparse and English in Go, Chinese in docs/UI).

## 进度记录规范

每次会话结束、任务完成或我要求暂停时，必须更新项目根目录的 `PROGRESS.md`：

1. 如果 `PROGRESS.md` 不存在，创建它。
2. 如果已存在，**追加**新条目，并更新“下一步”部分，不要覆盖历史记录。
3. 内容格式：

### YYYY-MM-DD
- **已完成**：简要描述本次会话完成的工作
- **当前状态**：待续 / 阻塞 / 完成
- **下一步**：具体的行动项

在开始新会话时，先读取 `PROGRESS.md` 了解上次进度，再继续工作。
