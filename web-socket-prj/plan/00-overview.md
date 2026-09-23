# Plan tổng — Realtime Chat Server (Go WebSocket)

Input: `idea.md`. Folder `plan/` tách mỗi phase thành 1 file, mỗi file đủ để implement độc lập (theo thứ tự).

---

## Trạng thái

| Phase | File | Trạng thái |
| --- | --- | --- |
| 1 — WebSocket Fundamentals | `phase-1-websocket-fundamentals.md` | ✅ Done |
| 2 — Concurrent Chat (Hub) | `phase-2-concurrent-chat.md` | ⬜ Todo |
| 3 — Room | `phase-3-room.md` | ⬜ Todo |
| 4 — Authentication (JWT) | `phase-4-authentication.md` | ⬜ Todo |
| 5 — Authorization (membership) | `phase-5-authorization.md` | ⬜ Todo |
| 6 — Multiple connections / user | `phase-6-multiple-connections.md` | ⬜ Todo |
| 7 — Heartbeat & Connection Health | `phase-7-heartbeat.md` | ⬜ Todo |
| 8 — Graceful Shutdown | `phase-8-graceful-shutdown.md` | ⬜ Todo |

---

## Kiến trúc đích (sau phase 8)

```text
                  HTTP request GET /ws?access_token=...
                                │
                                ▼
                  ┌───────────────────────────┐
                  │ ws.Handler (ServeHTTP)    │
                  │  - draining? → 503        │  phase 8
                  │  - validate JWT → 401     │  phase 4
                  │  - Upgrade → 101          │  phase 1
                  └─────────────┬─────────────┘
                                │ newClient(userID, conn)
                                ▼
       ┌────────────────────────────────────────────────┐
       │ Client (1 per connection)                      │
       │   readPump  goroutine  — decode, authorize     │
       │   writePump goroutine  — data + ping, 1 writer │
       │   send chan (buffered) + done chan             │
       └─────────────┬──────────────────────────────────┘
                     │ commands (register / join / leave / publish / unregister)
                     ▼
       ┌────────────────────────────────────────────────┐
       │ Hub (1 goroutine sở hữu toàn bộ state)          │
       │   clients map[*Client]struct{}                 │
       │   users   map[userID]map[*Client]struct{}      │  phase 6
       │   rooms   map[roomID]map[*Client]struct{}      │  phase 3
       └────────────────────────────────────────────────┘
                     ▲
                     │ IsMember(userID, roomID)
       ┌─────────────┴──────────────┐
       │ MembershipStore (interface) │  phase 5
       └────────────────────────────┘
```

---

## Cấu trúc thư mục đích

```text
web-socket-prj/
├── cmd/server/main.go
├── internal/
│   ├── ws/          # handler, client (read/write pump), hub, protocol
│   ├── auth/        # JWT verifier + dev token issuer          (phase 4)
│   └── membership/  # MembershipStore + in-memory impl         (phase 5)
├── web/index.html   # test client
├── plan/            # folder này
└── idea.md
```

---

## Quyết định xuyên suốt

| Câu hỏi | Chốt | Lý do |
| --- | --- | --- |
| Thư viện WebSocket | `github.com/gorilla/websocket v1.5.3` | Ổn định, API gần với protocol, dễ học frame/close/ping |
| Đồng bộ state | **1 goroutine Hub sở hữu map**, giao tiếp bằng channel | Không cần mutex cho map, không race; đổi lại hub là điểm nghẽn — bàn ở câu 36 |
| Ai được ghi vào `websocket.Conn` | Chỉ `writePump` (data + ping). `WriteControl` chỉ dùng khi cần close gấp | gorilla chỉ cho phép 1 concurrent writer |
| Slow client | Buffer đầy → hub **kick** client, không block broadcast | 1 client chậm không được làm chậm cả room |
| I/O chậm (DB, membership) | Làm ở `readPump`, **không** làm trong goroutine Hub | Hub block = toàn server block |
| JWT library | `github.com/golang-jwt/jwt/v5` (đã có trong module cache) | |
| Goroutine leak test | `go.uber.org/goleak v1.3.0` | |
| Test | `go test -race -count=1 ./...` luôn phải xanh sau mỗi phase | Acceptance criteria của idea.md |

---

## Protocol tổng hợp (sau phase 3+)

Client → Server:

```json
{ "type": "join_room",  "room_id": "room-123" }
{ "type": "leave_room", "room_id": "room-123" }
{ "type": "message",    "room_id": "room-123", "content": "Hello" }
{ "type": "ping" }
```

Server → Client:

```json
{ "type": "joined",  "room_id": "room-123" }
{ "type": "left",    "room_id": "room-123" }
{ "type": "message", "room_id": "room-123", "sender_id": "user-123", "content": "Hello", "sent_at": "..." }
{ "type": "pong" }
{ "type": "error",   "code": "FORBIDDEN", "message": "You cannot join this room" }
{ "type": "server_shutdown", "reconnect_after_ms": 3000 }
```

Close codes dùng trong project:

| Code | Tên | Khi nào |
| --- | --- | --- |
| 1000 | Normal Closure | Client/Server đóng bình thường |
| 1001 | Going Away | Server shutdown (phase 8) |
| 1007 | Invalid Frame Payload Data | Text frame không phải UTF-8 (phase 1) |
| 1008 | Policy Violation | Slow client bị kick (phase 2) |
| 1009 | Message Too Big | Vượt `MaxMessageSize` (phase 1) |
| 4001 | Token Expired | JWT hết hạn khi đang connect (phase 4) |
| 4002 | Too Many Connections | User vượt giới hạn device (phase 6) |
