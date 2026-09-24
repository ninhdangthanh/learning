# Phase 2 — Concurrent Chat (Hub)

Chuyển Echo Server → Chat Server: message của 1 client được broadcast tới **tất cả** client đang kết nối.

---

## Mục tiêu

```text
Client A ──{"message":"hi"}──► readPump(A) ──► hub.broadcast
                                                   │
                                  ┌────────────────┼────────────────┐
                                  ▼                ▼                ▼
                              A.send           B.send           C.send
                                  │                │                │
                              writePump(A)     writePump(B)     writePump(C)
```

Client lifecycle: `Connect → Register → Read → Broadcast → Disconnect → Unregister → Cleanup`.

---

## Các quyết định cần chốt

| Câu hỏi | Chốt | Lý do |
| --- | --- | --- |
| Hub đồng bộ bằng gì? | **1 goroutine `Hub.Run`** + channel `register/unregister/broadcast` (giống idea.md) | Chỉ 1 goroutine chạm map → không cần mutex, `-race` sạch. Phase này để học channel; mutex bàn ở cuối file |
| Sender có nhận lại message của mình? | **Có** | Client dùng làm "ack", UI đơn giản hơn; phase 6 cần để đồng bộ nhiều device |
| Ai đóng `send`? | **Không ai đóng `send`**. Dừng writer bằng `done chan struct{}` đóng qua `sync.Once` | Hub và readPump cùng ghi vào `send` → `close(send)` ở 1 phía sẽ gây panic "send on closed channel" ở phía kia |
| Slow client (buffer đầy)? | Hub dùng `select { case c.send <- m: default: kick(c) }`, kick = close 1008 | Broadcast không bao giờ block; đo được "backpressure" |
| Buffer size | `SendBufferSize = 64` (config) | Chịu được burst ngắn |
| Payload broadcast | Marshal **1 lần** ở hub, cùng `[]byte` gửi cho mọi client | Tránh marshal N lần; `[]byte` chỉ đọc nên share an toàn |
| Binary frame? | Phase này: trả `error UNSUPPORTED_MESSAGE_TYPE`, không broadcast | Chat là text protocol; binary echo chỉ để học phase 1 |

---

## Thay đổi code

### 1. `connection` → `Client` (`internal/ws/client.go`)

```go
type Client struct {
	id       uint64
	hub      *Hub
	conn     *websocket.Conn
	cfg      Config
	logger   *slog.Logger
	send     chan outboundFrame
	done     chan struct{}
	stopOnce sync.Once
}

func (c *Client) stop()                              // stopOnce.Do(close(done))
func (c *Client) trySend(frame outboundFrame) bool   // select send / done / default → false
```

- `writePump`: thêm `case <-c.done:` → gửi close frame (mã do người gọi `stop` quyết định, lưu trong field `closeCode` set **trước** khi `stop`) → return.
- `readPump` defer: `hub.unregister <- c` (không `close(send)` nữa).
- Lỗi decode (malformed): readPump gọi `c.trySend(errorFrame)` trực tiếp, không đi qua hub.

### 2. `Hub` (`internal/ws/hub.go`)

```go
type Hub struct {
	clients    map[*Client]struct{}
	register   chan *Client
	unregister chan *Client
	broadcast  chan broadcastMessage
	logger     *slog.Logger
}

type broadcastMessage struct {
	sender  *Client
	payload []byte
}

func NewHub(logger *slog.Logger) *Hub
func (h *Hub) Run(ctx context.Context)
```

`Run`:

```text
for select
  ctx.Done()          → return   (phase 8 mới xử lý đóng hết client)
  c := <-register     → clients[c] = {}
  c := <-unregister   → nếu còn trong map: delete + c.stop()
  m := <-broadcast    → for c in clients: if !c.trySend(m) → delete + kick(c, 1008)
```

`unregister` phải idempotent: client có thể bị kick (đã delete) rồi readPump vẫn gửi unregister.

### 3. `Handler`

```text
Upgrade → c := newClient(...) → hub.register <- c → go c.writePump() → c.readPump() → <-writerDone
```

`Handler` nhận `*Hub` qua constructor. `main.go`: `hub := ws.NewHub(logger); go hub.Run(ctx)`.

### 4. Protocol

Client → Server (giữ như phase 1):

```json
{ "message": "hi" }
```

Server → Client:

```json
{ "type": "message", "sender_id": "conn-3", "message": "hi", "sent_at": "2026-09-23T10:00:00Z" }
```

`sender_id` tạm là `conn-{id}` — phase 4 đổi thành `userID` lấy từ JWT.

### 5. `web/index.html`

Mở 2–3 tab, gửi từ 1 tab thấy ở tất cả. Hiển thị `sender_id`.

---

## Test

| Test | Kiểm tra |
| --- | --- |
| `TestBroadcastReachesAllClients` | 3 client, A gửi → A, B, C đều nhận đúng payload |
| `TestUnregisterOnDisconnect` | B đóng → hub còn 2 client; broadcast tiếp không lỗi |
| `TestSlowClientIsKicked` | Client không đọc, `SendBufferSize=1`, spam broadcast → nhận close 1008, các client khác vẫn nhận đủ |
| `TestConcurrentBroadcastNoRace` | 20 client gửi đồng thời 50 message mỗi client, `-race`, mỗi client nhận đủ 1000 |
| `TestHubUnregisterIsIdempotent` | Unit test hub: unregister 2 lần không panic |
| `TestBinaryFrameRejected` | Nhận `UNSUPPORTED_MESSAGE_TYPE` |

Test hub nên có `Hub.ClientCount()` đi qua channel (request/response) — **không** đọc `len(map)` trực tiếp từ goroutine test (sẽ race).

---

## Acceptance (idea.md §12 — Concurrency)

- [x] Nhiều client connect đồng thời
- [x] Message của 1 client broadcast tới nhiều client
- [x] Client đồng thời không làm hỏng shared state
- [x] Register/unregister concurrency-safe
- [x] `go test -race` không báo race

---

## Điểm cần tự giải thích được

1. **Race ở đâu dù mỗi TCP connection riêng biệt?** Mỗi connection có goroutine riêng, nhưng tất cả cùng ghi vào 1 `map` chung → `clients[c] = true` và `delete(clients, c)` đồng thời = data race (Go còn có thể `fatal error: concurrent map writes`).
2. **Channel vs Mutex:**
   - Channel/owner goroutine: state có 1 chủ, logic tuần tự dễ suy luận, nhưng hub thành điểm nghẽn và mọi query phải request/response.
   - `sync.RWMutex`: đọc song song nhanh, code ít hơn, nhưng dễ quên lock, dễ deadlock khi gọi ra ngoài lúc đang giữ lock (vd gửi vào channel đầy khi giữ lock).
3. **Buffered vs unbuffered `send`:** unbuffered → hub chờ writer của từng client → 1 client chậm làm chậm tất cả. Buffered + `default` → hấp thụ burst, vượt quá thì kick.
4. **Vì sao không `close(send)`:** nguyên tắc "chỉ bên gửi duy nhất mới được đóng channel"; ở đây có 2 bên gửi (hub + readPump).
5. Slow client ảnh hưởng broadcast thế nào (câu 33, 34 idea.md).
