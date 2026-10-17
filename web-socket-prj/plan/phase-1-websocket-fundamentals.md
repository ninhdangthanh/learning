# Phase 1 — WebSocket Fundamentals (Echo Server) ✅

Trạng thái: **đã implement**. File này ghi lại thiết kế thực tế để các phase sau build tiếp.

---

## Mục tiêu

```text
Browser ──HTTP GET /ws + Upgrade headers──► Go Server
        ◄──────── 101 Switching Protocols ──
        ◄═════════ WebSocket frames ═══════►   (full-duplex, cùng TCP connection)
```

- Client gửi `{"message":"hello"}` → server trả `{"message":"hello"}`.
- Binary frame → trả lại nguyên bytes.
- Phát hiện disconnect, message lỗi, ping/pong, cleanup.

---

## Code hiện tại

| File | Vai trò |
| --- | --- |
| `internal/ws/config.go` | `Config{WriteWait, PongWait, PingPeriod, MaxMessageSize, SendBufferSize}` + `DefaultConfig()` (10s / 60s / 54s / 4096 / 16) |
| `internal/ws/message.go` | `EchoMessage`, `ErrorMessage`, `decodeEchoMessage` (bắt thiếu field bằng `*string`) |
| `internal/ws/connection.go` | Lifecycle 1 connection: `readPump`, `writePump`, `enqueue`, `initiateClose` |
| `internal/ws/handler.go` | `Upgrade`, cấp `conn_id`, đếm `ActiveConnections()` |
| `internal/ws/handler_test.go` | 15 test (có `-race`) |
| `cmd/server/main.go` | Mux `GET /ws` + static `web/`, flag `-addr -web -debug` |
| `web/index.html` | Test client: JSON / binary / malformed / oversized |

---

## Thiết kế goroutine

```text
ServeHTTP goroutine (do net/http tạo)
  │
  ├── Upgrade  → hijack TCP conn, trả 101
  ├── go writePump()        ← goroutine #2: writer DUY NHẤT
  ├── readPump()            ← chạy luôn trên goroutine hiện tại
  │     defer close(send)   → báo writer dừng
  └── <-writerDone          ← chờ writer thoát rồi mới return → không leak
```

`writePump`:

```text
select
  frame, ok := <-send
      !ok        → gửi close 1000 (bỏ qua ErrCloseSent) → return
      else       → SetWriteDeadline + WriteMessage
  <-ticker.C     → gửi Ping
defer: ticker.Stop, conn.Close, close(writerDone)
```

`enqueue` luôn `select { send <- f ; <-writerDone }` → nếu writer chết trước (write lỗi), reader không bị block vĩnh viễn trên channel.

---

## Xử lý từng case

| Case | Cơ chế | Kết quả |
| --- | --- | --- |
| JSON hợp lệ | `decodeEchoMessage` | echo lại |
| JSON hỏng / thiếu `message` / sai kiểu | trả `ErrorMessage{MALFORMED_MESSAGE}` | **giữ** connection |
| Text không phải UTF-8 | `initiateClose(1007)` → set `closing=true`, read deadline = `WriteWait`, chờ client đáp close | đóng 1007 |
| Message > `MaxMessageSize` | `SetReadLimit` — gorilla tự gửi close 1009, `ReadMessage` trả `ErrReadLimit` | đóng 1009 |
| Client gửi close frame | default CloseHandler của gorilla tự đáp close; reader nhận `CloseError` | cleanup |
| TCP đứt không có close frame | `ReadMessage` trả `close 1006 unexpected EOF` | cleanup |
| Client không trả pong | `PongHandler` không gia hạn deadline → read timeout | cleanup |
| Client gửi ping | default PingHandler tự trả pong | — |
| Request thường không có Upgrade | `Upgrader.Upgrade` trả 400 | không tạo connection |

---

## Test đã có

`TestUpgradeRejectsPlainHTTPRequest`, `TestEchoesTextMessage`, `TestEchoesBinaryMessage`,
`TestMalformedMessageReturnsErrorAndKeepsConnectionOpen` (3 sub-case), `TestInvalidUTF8TextClosesWithInvalidPayloadCode`,
`TestOversizedMessageClosesWithMessageTooBigCode`, `TestServerAnswersClientPing`,
`TestServerSendsPingAndStaysAliveWhenClientPongs`, `TestDeadClientIsDisconnectedAfterPongTimeout`,
`TestClientCloseFrameCleansUpConnection`, `TestAbruptTCPDisconnectCleansUpConnection`,
`TestHandlesManyConcurrentConnections`.

Smoke test handshake bằng curl (key mẫu của RFC 6455):

```bash
curl -si -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
  -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
  localhost:8080/ws
# HTTP/1.1 101 Switching Protocols
# Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=   ← = base64(sha1(key + GUID))
```

---

## Acceptance (idea.md §12 — WebSocket)

- [x] Client establish WebSocket connection
- [x] Server xử lý HTTP Upgrade
- [x] Text message / Binary message
- [x] Server gửi message về
- [x] Detect disconnect (close frame, TCP drop, timeout)
- [x] Handle malformed message
- [x] Close connection đúng cách (close handshake)
- [x] Ping/Pong heartbeat

---

## Điểm cần tự giải thích được

1. `Sec-WebSocket-Accept` được tính thế nào, tại sao cần (chống cache/proxy trả nhầm response HTTP thường).
2. Sau `101`, TCP connection vẫn là connection cũ — chỉ đổi protocol nói trên nó.
3. Frame: FIN, opcode (0x1 text, 0x2 binary, 0x8 close, 0x9 ping, 0xA pong), mask (client → server bắt buộc mask).
4. Vì sao `ReadMessage()` một mình không đủ: không có deadline thì peer chết im lặng (rút cáp, NAT timeout) → goroutine treo mãi.
5. Vì sao cần writer duy nhất: `websocket.Conn` không cho concurrent write; ping và data cùng ghi.
6. Close handshake: bên đóng gửi close → bên kia đáp close → bên server đóng TCP.

---

## Việc để lại cho phase sau

- `connection` → đổi tên `Client`, gắn vào Hub (phase 2).
- `close(send)` bởi reader sẽ **không còn đúng** khi có Hub cùng gửi vào `send` → chuyển sang `done` channel + `sync.Once` (phase 2).
- `main.go` chỉ `server.Shutdown` — không đóng các connection đã hijack (phase 8).
