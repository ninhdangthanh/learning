# Phase 3 — Room

Thay broadcast toàn server bằng broadcast **theo room**. Client join/leave nhiều room trên cùng 1 connection.

---

## Mục tiêu

```text
Client A ── join room-123, room-456
Client B ── join room-123
Client C ── join room-456

A gửi message → room-123  ⇒ A, B nhận     (C không nhận)
A gửi message → room-456  ⇒ A, C nhận     (B không nhận)
```

---

## Các quyết định cần chốt

| Câu hỏi | Chốt | Lý do |
| --- | --- | --- |
| Room tạo thế nào? | **Lazy** — join lần đầu thì tạo, room rỗng thì xoá | Không cần API tạo room; phase 5 mới giới hạn ai được vào |
| Lưu state ở đâu? | Vẫn trong Hub: `rooms map[string]map[*Client]struct{}` + `Client.rooms map[string]struct{}` (chỉ hub goroutine đụng) | Giữ 1 owner; unregister cần biết client ở room nào để dọn nhanh O(rooms của client) |
| Gửi vào room chưa join? | `error NOT_IN_ROOM` | Tránh "gửi mù"; phase 5 nâng cấp thành authorization thật |
| Join room đã join? | Idempotent, vẫn trả `joined` | Client reconnect gửi lại join không bị lỗi |
| Validate `room_id` | Regex `^[a-z0-9][a-z0-9_-]{0,63}$` | Chặn rác, key log sạch |
| Giới hạn | `MaxRoomsPerClient = 50`, `MaxContentLength = 2000` rune | Chống abuse đơn giản |
| Kênh vào Hub | Gộp thành **1 channel `commands`** kiểu `hubCommand` | Thứ tự join → message của 1 client được giữ nguyên (2 channel khác nhau thì `select` có thể đảo thứ tự) |
| Có ack không? | Có: `joined` / `left` | Client biết lúc nào an toàn để gửi message |

---

## Protocol

Envelope chung, decode 2 bước (đọc `type` trước, rồi decode phần còn lại):

```go
type inboundEnvelope struct {
	Type    string `json:"type"`
	RoomID  string `json:"room_id"`
	Content string `json:"content"`
}
```

| `type` | Field bắt buộc | Response |
| --- | --- | --- |
| `join_room` | `room_id` | `{"type":"joined","room_id":"..."}` |
| `leave_room` | `room_id` | `{"type":"left","room_id":"..."}` |
| `message` | `room_id`, `content` | broadcast `{"type":"message","room_id","sender_id","content","sent_at"}` tới room |

Error codes mới:

| Code | Khi nào |
| --- | --- |
| `MALFORMED_MESSAGE` | JSON hỏng / thiếu field |
| `UNKNOWN_TYPE` | `type` không hỗ trợ |
| `INVALID_ROOM_ID` | Sai regex |
| `NOT_IN_ROOM` | Gửi message / leave room chưa join |
| `TOO_MANY_ROOMS` | Vượt `MaxRoomsPerClient` |
| `CONTENT_TOO_LONG` | Vượt `MaxContentLength` |

Error nên kèm `room_id` (nếu có) và echo `type` gốc để client map lỗi với request.

---

## Thay đổi code

### 1. `internal/ws/protocol.go` (thay `message.go`)

- `inboundEnvelope`, `outboundMessage`, `outboundAck`, `ErrorMessage`.
- `decodeInbound(payload) (inboundEnvelope, *ErrorMessage)` — validate cú pháp + regex + độ dài tại **readPump** (không tốn thời gian của hub).

### 2. Hub

```go
type commandKind int

const (
	commandRegister commandKind = iota
	commandUnregister
	commandJoin
	commandLeave
	commandPublish
)

type hubCommand struct {
	kind    commandKind
	client  *Client
	roomID  string
	payload []byte
}
```

```text
Run: for cmd := range commands (+ ctx.Done)
  join      → rooms[room][c]; c.rooms[room]; trySend(joined)
  leave     → nếu không có: NOT_IN_ROOM; else xoá 2 chiều; room rỗng → delete(rooms, room); trySend(left)
  publish   → nếu c không ở room: NOT_IN_ROOM; else fan-out tới rooms[room]
  unregister→ với mỗi room trong c.rooms: xoá c, room rỗng → delete; delete clients; c.stop()
```

Payload `message` được marshal ở **readPump** (đã có `sender_id`, `sent_at`) rồi đưa `[]byte` vào hub → hub chỉ fan-out.

### 3. Metrics debug

`Hub.Stats() Stats{Clients, Rooms int}` qua request/response channel. Expose `GET /debug/stats` (JSON) để quan sát room rỗng bị dọn.

### 4. `web/index.html`

Input room_id + nút Join/Leave, danh sách room đã join, gửi message chọn room.

---

## Test

| Test | Kiểm tra |
| --- | --- |
| `TestJoinRoomReturnsAck` | Nhận `joined` |
| `TestMessageOnlyReachesRoomMembers` | Kịch bản A/B/C ở trên; C **không** nhận (đọc với deadline ngắn, expect timeout) |
| `TestClientInMultipleRooms` | A nhận message từ cả 2 room, `room_id` đúng |
| `TestLeaveRoomStopsDelivery` | Leave rồi không nhận nữa |
| `TestSendToRoomNotJoined` | `NOT_IN_ROOM` |
| `TestEmptyRoomIsCleanedUp` | Join rồi leave / disconnect → `Stats().Rooms == 0` |
| `TestDisconnectRemovesClientFromAllRooms` | Client ở 3 room disconnect → cả 3 room được dọn |
| `TestInvalidRoomID` / `TestUnknownType` / `TestContentTooLong` | Error code đúng, connection vẫn mở |
| `TestJoinThenMessageOrderPreserved` | Gửi join + message liền nhau không chờ ack → message vẫn được deliver (chứng minh 1 channel giữ thứ tự) |

Helper test nên có `joinRoom(t, conn, room)` chờ ack, và `expectNoMessage(t, conn, 200ms)`.

---

## Acceptance (idea.md §12 — Rooms)

- [ ] Client join room
- [ ] Client leave room
- [ ] Message chỉ tới member của room
- [ ] Client thuộc nhiều room
- [ ] Room rỗng được dọn

---

## Điểm cần tự giải thích được

1. Vì sao dùng message `type` thay vì REST `/join`, `/send` (1 connection, full-duplex, không tốn handshake mỗi lệnh).
2. Vì sao cần index 2 chiều (room → clients và client → rooms).
3. Vì sao gộp các lệnh vào 1 channel (thứ tự của `select` giữa nhiều channel ready là **ngẫu nhiên**).
4. Room rỗng không dọn → memory leak chậm (mỗi room_id rác một entry).
5. Validate ở readPump, fan-out ở hub: tách việc song song hoá được (per-connection) khỏi việc bắt buộc tuần tự (state chung).
