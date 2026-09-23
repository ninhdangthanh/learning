# Phase 6 — Multiple Connections per User

1 user = nhiều device = nhiều WebSocket connection. Đóng 1 device không ảnh hưởng device khác.

---

## Mục tiêu

```text
              user-123
                  │
        ┌─────────┼─────────┐
        ▼         ▼         ▼
     Client#1  Client#2  Client#3
     (laptop)  (mobile)  (tablet)
```

Hub có thêm index:

```go
users map[string]map[*Client]struct{}
```

Không dùng `map[userID]*Client` — connection thứ 2 sẽ ghi đè connection thứ 1, connection cũ thành "mồ côi" (vẫn sống, không ai dọn, không nhận message user-level).

---

## Các quyết định cần chốt

| Câu hỏi | Chốt | Lý do |
| --- | --- | --- |
| Join room theo connection hay theo user? | **Theo connection** — mỗi device tự gửi `join_room` | Device có thể mở màn hình khác nhau; đơn giản, đã có sẵn từ phase 3. Ghi rõ alternative (per-user auto join) trong README |
| Giới hạn số connection / user | `MaxConnectionsPerUser = 5`; vượt → connection **mới** bị đóng 4002 `too many connections` | Chống 1 user mở vô hạn tab/bot. Không đá connection cũ vì dễ tạo vòng lặp reconnect giữa 2 device |
| Check giới hạn ở đâu? | Trong hub khi `register` (sau Upgrade) | Check trước Upgrade cần query hub rồi mới upgrade → race giữa 2 connection đồng thời; check trong hub là tuần tự nên chính xác |
| Message user-level | `Hub.SendToUser(userID, payload)` gửi tới mọi connection của user | Nền cho notification ("bạn bị remove khỏi room", DM sau này) |
| Presence | Hub phát `{"type":"presence","user_id","status":"online|offline"}` vào các room user đang ở, **chỉ khi** connection đầu tiên vào / connection cuối cùng rời | Đóng 1 trong 3 tab không được báo "offline" |
| Revoke (phase 5) | Dùng `users[userID]` thay vì duyệt cả room | O(connection của user) |

---

## Thay đổi code

### 1. Hub state

```go
type Hub struct {
	clients map[*Client]struct{}
	users   map[string]map[*Client]struct{}
	rooms   map[string]map[*Client]struct{}
	...
}
```

Invariant (viết thành hàm `checkInvariants()` chỉ gọi trong test):

```text
c ∈ clients                ⇔ c ∈ users[c.userID]
c ∈ rooms[r]               ⇔ r ∈ c.rooms
users[u] tồn tại            ⇒ len(users[u]) > 0
rooms[r] tồn tại            ⇒ len(rooms[r]) > 0
```

### 2. Register / Unregister

```text
register(c):
  conns := users[c.userID]
  len(conns) >= MaxConnectionsPerUser → c.closeWith(4002) ; return
  first := len(conns) == 0
  add vào clients, users
  (presence online phát khi c join room đầu tiên mà user chưa online ở room đó — xem dưới)

unregister(c):
  với mỗi room r trong c.rooms: remove; nếu user không còn connection nào khác trong r → presence offline vào r
  delete clients[c]; delete users[u][c]; users[u] rỗng → delete(users, u)
  c.stop()
```

Presence tính **theo room**: "user X còn connection nào trong room r không" = duyệt `users[X]` xem client nào có `r` trong `rooms` (số device nhỏ nên rẻ).

### 3. `GET /debug/stats`

```json
{ "clients": 7, "users": 3, "rooms": 2,
  "connections_per_user": { "user-a": 3, "user-b": 2, "user-c": 2 } }
```

---

## Test

| Test | Kiểm tra |
| --- | --- |
| `TestSameUserMultipleConnections` | 3 connection cùng token → stats `users=1, clients=3` |
| `TestAllDevicesInRoomReceiveMessage` | 3 device cùng join room, user khác gửi → cả 3 nhận |
| `TestSenderOtherDevicesReceiveOwnMessage` | Device 1 gửi → device 2, 3 cũng thấy (đồng bộ lịch sử giữa các device) |
| `TestClosingOneDeviceKeepsOthers` | Đóng device 1 → device 2, 3 vẫn gửi/nhận |
| `TestUserStateCleanedWhenLastDisconnects` | Đóng hết → `users` không còn key |
| `TestConnectionLimitPerUser` | Connection thứ 6 nhận close 4002, 5 cái cũ vẫn sống |
| `TestPresenceOnlyOnFirstAndLast` | Mở 2 device cùng room: người khác nhận 1 `online`; đóng 1 device: không có `offline`; đóng device cuối: nhận `offline` |
| `TestRevokeKicksAllDevices` | 3 device trong room, revoke → cả 3 nhận `removed` |
| `TestHubInvariantsUnderChurn` | 50 goroutine connect/join/leave/disconnect ngẫu nhiên, cuối cùng `checkInvariants()` pass, `-race` |

---

## Acceptance (idea.md §12 — Multiple connections)

- [ ] 1 user có nhiều WebSocket connection
- [ ] Ngắt 1 connection không ngắt connection khác
- [ ] State của user được dọn đúng

---

## Điểm cần tự giải thích được

1. Vì sao `map[userID]*Client` sai (câu 31).
2. Presence "first in / last out" và vì sao phải tính theo room.
3. Giới hạn connection: reject mới vs đá cũ — trade-off.
4. Vì sao check giới hạn trong hub thay vì trước Upgrade (TOCTOU race).
5. Index nhiều chiều → phải giữ invariant khi xoá; bug dọn dẹp thường nằm ở đây.
