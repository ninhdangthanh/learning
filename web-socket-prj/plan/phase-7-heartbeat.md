# Phase 7 — Heartbeat & Connection Health

Phase 1 đã có protocol-level ping/pong + read/write deadline. Phase này làm **chắc** và **quan sát được**: cấu hình, heartbeat phía client, reconnect, đo lường, test goroutine leak.

---

## Hiện trạng (từ phase 1)

| Có rồi | Ở đâu |
| --- | --- |
| Ping interval (`PingPeriod = 54s`) | `writePump` ticker |
| Pong handler gia hạn read deadline | `readPump` → `SetPongHandler` |
| Read deadline (`PongWait = 60s`) | `extendReadDeadline` |
| Write deadline (`WriteWait = 10s`) | `write()` |
| Close dead connection | read timeout → readPump return → cleanup |

Còn thiếu: cấu hình được, client biết server chết, reconnect, metrics, chống leak có kiểm chứng.

---

## Các quyết định cần chốt

| Câu hỏi | Chốt | Lý do |
| --- | --- | --- |
| Cấu hình timing | Flag + env: `-ping-period`, `-pong-wait`, `-write-wait`; validate `PingPeriod < PongWait` khi start | Deploy sau load balancer (ALB idle timeout mặc định 60s) cần chỉnh ping < idle timeout |
| Default | `PongWait=60s`, `PingPeriod=25s`, `WriteWait=10s` | Ping 25s < mọi idle timeout phổ biến (ALB 60s, nginx 60s), vẫn chịu được 1 lần ping mất |
| Browser phát hiện server chết thế nào? | **Application heartbeat**: client gửi `{"type":"ping"}` mỗi 20s, server trả `{"type":"pong"}`; 2 lần không có pong → client tự đóng + reconnect | Browser tự trả pong cho protocol ping nhưng **JS không thấy** ping/pong frame và không gửi được ping frame |
| App ping có gia hạn read deadline? | Có — mọi frame đọc được đều gọi `extendReadDeadline` | Client đang gửi dữ liệu thì chắc chắn còn sống |
| Reconnect | Client: exponential backoff 1s → 2s → 4s … max 30s + jitter ±20%; sau reconnect tự `join_room` lại các room cũ | Tránh "thundering herd" khi server restart (phase 8) |
| Metrics | `expvar` tại `/debug/vars`: `ws_active_connections`, `ws_dead_connections_total`, `ws_slow_client_kicks_total`, `ws_messages_in_total`, `ws_messages_out_total` | Có sẵn trong stdlib, không thêm dependency |
| TCP keepalive | Để mặc định của Go (15s) — ghi chú khác biệt với WS ping | Keepalive chỉ biết TCP còn sống, không biết app còn đọc hay không |
| Goroutine leak test | `go.uber.org/goleak` `VerifyTestMain` | Bắt goroutine sót sau mỗi test package |

---

## Thay đổi code

### 1. `Config` + validate

```go
func (c Config) Validate() error
```

`PingPeriod >= PongWait` → lỗi; `WriteWait <= 0` → lỗi. `main.go` fail sớm.

### 2. Phân loại lý do đóng

```go
type disconnectReason string

const (
	reasonClientClosed   disconnectReason = "client_closed"
	reasonPongTimeout    disconnectReason = "pong_timeout"
	reasonWriteFailed    disconnectReason = "write_failed"
	reasonSlowClient     disconnectReason = "slow_client"
	reasonProtocolError  disconnectReason = "protocol_error"
	reasonTokenExpired   disconnectReason = "token_expired"
	reasonServerShutdown disconnectReason = "server_shutdown"
)
```

`logReadError` hiện có → trả `disconnectReason`; log 1 dòng `connection closed reason=... duration=...` + tăng counter tương ứng.

### 3. App-level ping

`type: "ping"` trong protocol → readPump `trySend(pong)` trực tiếp, không qua hub.

### 4. `web/index.html`

- Heartbeat timer 20s, đếm pong miss.
- Reconnect backoff + jitter, hiển thị trạng thái `reconnecting in 4s (attempt 3)`.
- Lưu danh sách room đã join để re-join.

### 5. Test tool: giả lập mạng xấu

`internal/ws/testutil` có `blackholeConn`: wrap `net.Conn`, sau khi bật thì `Read` block và `Write` nuốt dữ liệu → mô phỏng rút cáp (không có FIN/RST). Dùng `Dialer.NetDialContext` để chèn.

---

## Test

| Test | Kiểm tra |
| --- | --- |
| `TestConfigValidate` | Các combo sai bị reject |
| `TestBlackholedClientDetectedWithinPongWait` | Bật blackhole → server đóng trong khoảng `PongWait + ε`, reason `pong_timeout` |
| `TestActiveClientNeverTimesOut` | Client chỉ gửi app ping (không trả protocol pong) → vẫn sống nhờ gia hạn deadline khi đọc frame |
| `TestAppPingReturnsPong` | `{"type":"ping"}` → `{"type":"pong"}` |
| `TestWriteDeadlineClosesStuckWriter` | Client không đọc + TCP buffer đầy → write timeout → cleanup, reason `write_failed` hoặc `slow_client` |
| `TestMetricsCountDisconnectReasons` | Counter tăng đúng |
| `TestMain` với `goleak.VerifyTestMain(m)` | Không goroutine nào sót sau cả package |

---

## Acceptance (idea.md §12 — Reliability, một phần)

- [ ] Dead connection được phát hiện
- [ ] Ping/Pong timeout hoạt động
- [ ] Server xử lý client disconnect bất thường
- [ ] Không có goroutine leak trong lifecycle bình thường

---

## Điểm cần tự giải thích được

1. Vì sao `conn.ReadMessage()` không đủ cho production (idea.md §9): half-open TCP connection có thể tồn tại hàng giờ.
2. TCP biết mất kết nối bằng cách nào (FIN, RST, keepalive, retransmission timeout) và khi nào **không** biết (câu 13, 14).
3. Protocol ping (0x9/0xA) vs app ping (`{"type":"ping"}`) — ai thấy cái nào.
4. Ping interval liên quan gì tới load balancer idle timeout.
5. Write deadline cứu gì: client không đọc → TCP send buffer đầy → `Write` block mãi.
6. Detect goroutine leak thế nào (câu 21): `goleak`, `runtime.NumGoroutine`, `pprof/goroutine`.
7. Thundering herd khi reconnect và vì sao cần jitter.
