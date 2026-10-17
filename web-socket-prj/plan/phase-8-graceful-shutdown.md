# Phase 8 — Graceful Shutdown

Nhận `SIGTERM` thì không cắt ngang mọi connection: ngừng nhận mới, báo client, đóng lịch sự, chờ goroutine, rồi mới tắt.

---

## Mục tiêu

```text
SIGTERM
   ↓
1. draining = true            → /ws mới trả 503 + Retry-After
   ↓
2. server.Shutdown(ctx)       → đóng listener, chờ HTTP request thường
   ↓                            (KHÔNG chờ connection đã hijack — WebSocket)
3. hub.Shutdown()             → mọi client: gửi {"type":"server_shutdown","reconnect_after_ms":N}
   ↓                            rồi close frame 1001 Going Away
4. connections.Wait()         → chờ readPump/writePump của mọi client thoát
   ↓  (timeout 10s)
5. timeout → force conn.Close() với client còn sót
   ↓
6. hub goroutine dừng, main return
```

---

## Vì sao `server.Shutdown` là chưa đủ

`http.Server.Shutdown` chờ các connection **idle** và **đang xử lý request**, nhưng connection đã bị `Hijack()` (Upgrader làm việc này) nằm **ngoài** quản lý của `http.Server`. Doc Go ghi rõ: *"Shutdown does not attempt to close nor wait for hijacked connections such as WebSockets"*. Không tự xử lý → process thoát, client nhận 1006 abnormal closure, không có thông báo.

---

## Các quyết định cần chốt

| Câu hỏi | Chốt | Lý do |
| --- | --- | --- |
| Đếm connection bằng gì? | `sync.WaitGroup` trong `Handler`: `wg.Add(1)` ngay sau Upgrade, `defer wg.Done()` | Handler goroutine chỉ return sau khi cả reader + writer thoát (đã đảm bảo từ phase 1) |
| Tránh `wg.Add` sau khi `Wait` bắt đầu | Kiểm `draining` (atomic.Bool) **trước** Upgrade; `Add` và check nằm dưới cùng 1 mutex nhỏ `admitMu` | `Add` đồng thời với `Wait` khi counter = 0 là misuse của WaitGroup |
| Thông báo client | App message `server_shutdown` + close **1001** | Client phân biệt được "server restart, reconnect sau" với lỗi mạng |
| `reconnect_after_ms` | Random 1000–5000ms mỗi client | Rải reconnect, tránh thundering herd vào instance mới |
| Timeout tổng | `ShutdownTimeout = 15s` (flag); đủ nhỏ hơn ECS/K8s `stopTimeout` (30s mặc định) | Quá hạn thì platform SIGKILL, mất hết |
| Chờ client đáp close? | Có, mỗi client tối đa `WriteWait`; sau đó đóng TCP | Close handshake đúng chuẩn |
| Hub dừng thế nào? | `ctx` của hub cancel **sau** khi mọi client đã unregister | Nếu hub dừng trước, readPump gửi `unregister` vào channel không ai đọc → treo → WaitGroup không bao giờ về 0 |
| readPump gửi command khi hub đã dừng | Mọi lần gửi vào hub đều `select { case hub.commands <- cmd: case <-hub.done: }` | Phòng thủ thêm cho case trên |
| Health check khi draining | `GET /healthz` trả 503 khi draining | Load balancer ngừng route vào instance đang tắt |

---

## Thay đổi code

### 1. `Handler`

```go
type Handler struct {
	...
	admitMu     sync.Mutex
	draining    bool
	connections sync.WaitGroup
}

func (h *Handler) admit() bool
func (h *Handler) BeginDrain()
func (h *Handler) WaitConnections(ctx context.Context) error
```

`ServeHTTP`: `if !h.admit() { 503 + Retry-After: 5; return }` → verify JWT → Upgrade → `defer h.connections.Done()`.
Nếu Upgrade lỗi phải `Done()` ngay (vì đã `Add` trong `admit`).

### 2. `Hub.Shutdown`

```go
func (h *Hub) Shutdown(ctx context.Context) error
```

Gửi command `commandShutdown` vào hub → hub với mỗi client: `trySend(server_shutdown)`, `closeWith(1001)`. Hub vẫn chạy để nhận `unregister`. Trả về khi `len(clients) == 0` hoặc `ctx` hết hạn.

### 3. `Client`

- `closeWith(code, reason)` set close code rồi `stop()` — writePump gửi message còn trong buffer (drain `send` non-blocking) → close frame → chờ reader nhận close reply tối đa `WriteWait` → `conn.Close()`.
- Force close: `Hub.ForceCloseAll()` gọi `conn.Close()` thẳng → readPump nhận lỗi ngay.

### 4. `main.go`

```text
ctx, stop := signal.NotifyContext(SIGINT, SIGTERM)
<-ctx.Done()
shutdownCtx := WithTimeout(ShutdownTimeout)
handler.BeginDrain()
server.Shutdown(shutdownCtx)
hub.Shutdown(shutdownCtx)
if handler.WaitConnections(shutdownCtx) != nil → hub.ForceCloseAll(); handler.WaitConnections(1s)
cancelHub(); <-hub.Done()
```

Log mỗi bước kèm số connection còn lại + thời gian.

### 5. `web/index.html`

Nhận `server_shutdown` → hiển thị "server restarting" → reconnect sau `reconnect_after_ms` (dùng lại backoff của phase 7).

---

## Test

| Test | Kiểm tra |
| --- | --- |
| `TestDrainRejectsNewConnections` | Sau `BeginDrain`, dial → 503 |
| `TestShutdownNotifiesAndClosesAllClients` | 10 client (nhiều room, nhiều user) → mỗi client nhận `server_shutdown` rồi close 1001 |
| `TestShutdownWaitsForConnectionGoroutines` | Sau `WaitConnections` trả nil → `ActiveConnections()==0` |
| `TestShutdownForceClosesUnresponsiveClient` | Client blackhole (phase 7) → shutdown vẫn xong trong `timeout + ε` |
| `TestShutdownWithConcurrentConnects` | Liên tục dial trong lúc shutdown → không panic WaitGroup, `-race` sạch, mọi dial hoặc 503 hoặc nhận 1001 |
| `TestNoGoroutineLeakAfterShutdown` | `goleak.VerifyNone(t)` sau toàn bộ shutdown |
| Manual | `make run`, mở 3 tab, `kill -TERM <pid>` → cả 3 tab hiện "server restarting" và reconnect khi server lên lại |

---

## Acceptance (idea.md §12 — Reliability)

- [ ] Server graceful shutdown
- [ ] Không goroutine leak (kể cả sau shutdown)

---

## Điểm cần tự giải thích được

1. Vì sao `http.Server.Shutdown` không đóng WebSocket (hijack).
2. `context.Context` dùng để làm gì trong shutdown (câu 22): lan truyền deadline + cancel xuống mọi bước.
3. `sync.WaitGroup` giải quyết gì (câu 23) và lỗi "Add sau Wait".
4. Thứ tự dừng: listener → client → hub. Đảo thứ tự thì chuyện gì xảy ra.
5. Server restart thì client thấy gì (câu 35): có graceful → 1001 + reconnect có kế hoạch; không có → 1006 + reconnect đồng loạt.
6. Rolling deploy trên ECS/K8s: `stopTimeout`, deregistration delay của ALB, health check 503 khi draining.
7. Câu 36 — 10,000 connection thì kiến trúc này có vấn đề gì: 1 hub goroutine là nút thắt (→ shard hub theo room), 2 goroutine/connection (~ 8KB stack mỗi cái + buffer), broadcast room lớn O(n) trên 1 goroutine, và **1 instance** — scale ngang cần pub/sub (Redis/NATS) giữa các instance + sticky session hoặc không cần sticky nếu mọi instance subscribe room.
