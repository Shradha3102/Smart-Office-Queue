# Smart Office Queue & Token Management System

Flutter (visitor kiosk + staff console + admin dashboard) · Go REST/WebSocket API · PostgreSQL

```
smart-queue/
├── backend/                 Go API (Go 1.24+, stdlib router, lib/pq, JWT, gorilla/websocket)
│   ├── cmd/server/          main: config, migrations, seeding, HTTP server, housekeeping
│   └── internal/
│       ├── queue/           ALL queue rules (tokens, priority, no-show, transfer, pause, ETA, dashboard)
│       ├── httpapi/         REST handlers, auth middleware, CORS, rate limiting
│       ├── realtime/        WebSocket hub that pushes "queue changed" events
│       ├── auth/            PBKDF2 password hashing, JWT, staff accounts
│       ├── db/migrations/   SQL schema (applied automatically on start)
│       └── seed/            default departments + demo accounts
├── app/                     Flutter app (Android, iOS, Web, Windows, macOS, Linux)
│   └── lib/
│       ├── api/             typed API client + models
│       ├── services/        session, realtime (auto-reconnect), saved visitor tokens
│       ├── screens/visitor/ take token, live token tracking, my tokens
│       ├── screens/staff/   login, console, dashboard, user management
│       └── screens/display_board_screen.dart   lobby TV "Now serving" board
└── docker-compose.yml       PostgreSQL + API
```

---

## 1. Quick start

### Backend (Docker - easiest)

```bash
docker compose up -d --build        # PostgreSQL + API on http://localhost:8080
curl http://localhost:8080/api/health
```

### Backend (without Docker)

```bash
# PostgreSQL 13+ running locally, then:
createdb smartqueue
cd backend
cp .env.example .env                # edit if needed, then export the variables
go run ./cmd/server                 # migrations + seeding run automatically
```

### Flutter app

```bash
cd app
./setup.sh            # Windows: .\setup.ps1   (generates platform folders + flutter pub get)
flutter run -d chrome
# Android emulator talks to your PC via 10.0.2.2 (already the default).
# Real phone on Wi-Fi:
flutter run --dart-define=API_BASE_URL=http://192.168.1.50:8080
```

The server address can also be changed at runtime from the ⚙ icon on the home screen.

### Demo accounts (created on first start)

| Role | Email | Password | Access |
|---|---|---|---|
| Admin | admin@office.local | Admin@123 | every department, dashboard, users, settings |
| Staff - IT Support | it@office.local | Staff@123 | IT queue only |
| Staff - HR | hr@office.local | Staff@123 | HR queue only |
| Staff - Accounts | accounts@office.local | Staff@123 | Accounts queue only |
| Staff - Administration | administration@office.local | Staff@123 | Administration queue only |

Change these via `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `STAFF_PASSWORD`, `SEED_DEMO_STAFF=false`.

---

## 2. Features → requirements

| Requirement | Where / how |
|---|---|
| Visitor token generation | *Get a token* screen → `POST /api/tokens` |
| Service selection (IT Support, HR, Accounts, Administration) | Seeded departments `IT`, `HR`, `ACC`, `ADM` |
| Unique token numbers `IT-001`, `HR-002` | Per-department, per-day counter row locked inside the same transaction; resets daily (business timezone) |
| Live queue position | Computed from `(priority_lane DESC, queue_seq ASC)`; pushed live over WebSocket |
| Estimated waiting time | Remaining time of the visitor at the counter + people ahead × average service time (learned from the last 20 completions, falls back to the department default) |
| Token cancellation | Visitor (with the secret key returned at creation) or staff |
| Admin / Staff login | JWT; staff are scoped to their department, admins see all |
| Department-wise queues | Each department has its own queue, counter and numbering |
| Call next / Complete / No show | Staff console buttons |
| Transfer to another department | Token gets a new number in the target department (`IT-004 → HR-012`), keeps priority, joins the end of its lane; visitor keeps tracking the same token |
| Pause / resume department | Switch in the console header (optional reason shown to visitors) |
| Priority tokens | Visitor can request priority with a reason; staff can grant/remove it or issue priority tokens at the counter |
| Dashboard | Waiting, currently serving, completed, no-shows, average waiting time (+ avg service time, issued, cancelled, longest wait) - live |
| Extras | Lobby display board, "My tokens", staff-issued tokens, user management, duplicate-phone guard, rate limiting, end-of-day expiry |

## 3. Special logic (exact behaviour)

* **Priority never interrupts service.** Waiting order is `priority lane first, then arrival order`. Only *waiting* tokens are reordered - the visitor at the counter stays there. `Call next` is refused (`409 already_serving`) until the current token is completed / no-show / transferred / cancelled, so nobody can be skipped or double-called.
* **1st no-show → end of the queue.** The token gets a fresh queue sequence (behind everyone currently waiting, including later arrivals) and leaves the priority lane - a priority visitor who didn't turn up does not jump ahead again.
* **2nd no-show → automatically cancelled** (status `no_show`, reason recorded).
* **Paused department:** new tokens are rejected (`409 department_paused`), transfers *into* it are rejected and `Call next` is blocked. Visitors already waiting keep their place and see a "paused" banner. The current visitor can still be completed.
* **Live updates:** every change (create, call, complete, no-show, cancel, transfer, priority, pause/resume, expiry) is broadcast over `/ws`; clients refetch the affected department, so positions and ETAs update instantly. A 20-30 s poll is kept as a fallback, and the client reconnects with exponential backoff.
* **Concurrency safety:** every mutation locks its department row (`SELECT … FOR UPDATE`, two departments are locked in id order for transfers). A partial unique index guarantees at most one *serving* token per department. Tested with 30 parallel token requests and 8 parallel "Call next" clicks.
* **End of day:** tokens left waiting from a previous business day are expired automatically.

## 4. API reference

All responses are JSON. Errors: `{"error": {"code": "department_paused", "message": "…"}}`.

**Public**

| Method | Path | Body / notes |
|---|---|---|
| GET | `/api/health` | DB status |
| GET | `/api/departments` | departments + `waiting_count`, `now_serving`, `estimated_wait_minutes`, `is_paused` |
| POST | `/api/tokens` | `{department_id, visitor_name, visitor_phone?, is_priority?, priority_reason?}` → token + `access_secret` |
| GET | `/api/tokens/{id}` | token with `position`, `people_ahead`, `estimated_wait_minutes`, `now_serving` |
| POST | `/api/tokens/{id}/cancel` | `{access_secret}` (or header `X-Token-Secret`) |
| GET | `/ws` | WebSocket: `{"type":"queue.updated","action":"called","department_ids":[1],"token_id":"…"}` |

**Staff** (`Authorization: Bearer <jwt>`)

| Method | Path | Body |
|---|---|---|
| POST | `/api/auth/login` | `{email, password}` |
| GET | `/api/auth/me` | |
| GET | `/api/staff/departments/{id}/queue` | serving, waiting (ordered, with ETA), recently closed |
| POST | `/api/staff/departments/{id}/call-next` | |
| POST | `/api/staff/departments/{id}/pause` | `{reason?}` |
| POST | `/api/staff/departments/{id}/resume` | |
| POST | `/api/staff/tokens` | issue a token at the counter (same body as public) |
| POST | `/api/staff/tokens/{id}/complete` | |
| POST | `/api/staff/tokens/{id}/no-show` | → `{token, action: "requeued" \| "cancelled"}` |
| POST | `/api/staff/tokens/{id}/transfer` | `{department_id, note?}` |
| POST | `/api/staff/tokens/{id}/priority` | `{is_priority, reason}` |
| POST | `/api/staff/tokens/{id}/cancel` | `{reason?}` |
| GET | `/api/staff/dashboard?department_id=` | today's statistics |

**Admin**: `GET/POST /api/admin/users`, `PATCH /api/admin/users/{id}` `{is_active}`, `PATCH /api/admin/departments/{id}` `{name?, description?, avg_service_minutes?}`.

## 5. Tests

The backend has integration tests that run against a real PostgreSQL (each test gets its own throw-away database):

```bash
cd backend
TEST_DATABASE_URL="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable" go test -race ./...
```

Covered: numbering & daily reset, concurrent generation, priority ordering without interrupting service, no-show requeue → auto-cancel, priority no-show, paused rules, transfers (numbering, priority, permissions, serving tokens), ETA math and updates, visitor cancel with secret, concurrent call-next, permissions/validation, dashboard figures, end-of-day expiry, and a full HTTP + WebSocket end-to-end flow.

Flutter: `cd app && flutter analyze && flutter test`.

## 6. Production notes

* Set a strong `JWT_SECRET` (≥ 32 chars), restrict `CORS_ORIGINS`, and put the API behind HTTPS (`TRUST_PROXY=true` behind a reverse proxy). Use `https://`/`wss://` URLs in the app; the Android manifest allows cleartext HTTP only for LAN demos.
* `flutter build web` then start the API with `WEB_DIR=../app/build/web` to serve the app and API from one origin.
* Configuration reference: `backend/.env.example`.
