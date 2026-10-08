# Phase 5 — Authorization (Room Membership)

Đã biết **ai** (phase 4). Giờ quyết định người đó **được làm gì**: chỉ join / gửi message vào room mình là member.

---

## Mục tiêu

```text
user-A
  ├── restaurant-001 ✓
  └── restaurant-002 ✗

{"type":"join_room","room_id":"restaurant-002"}
        │
        ▼
Is user-A a member of restaurant-002?  → No
        │
        ▼
{"type":"error","code":"FORBIDDEN","room_id":"restaurant-002","message":"You cannot join this room"}
```

---

## Các quyết định cần chốt

| Câu hỏi | Chốt | Lý do |
| --- | --- | --- |
| Membership lưu ở đâu? | Interface `MembershipStore` + impl in-memory seed từ `data/memberships.json` | Không kéo DB vào project học; đổi sang Postgres/Redis chỉ cần impl mới |
| Check ở đâu? | **readPump** gọi store (có thể là I/O) **trước** khi gửi command `join` vào hub | Hub là 1 goroutine — gọi DB trong hub sẽ block toàn server |
| Check khi `message`? | Hub kiểm "client đã join room" (in-memory, rẻ). Không gọi store mỗi message | Join đã được authorize; gọi store mỗi message tốn kém. Revoke xử lý riêng (dưới) |
| Membership bị thu hồi khi đang ở trong room? | `POST /admin/rooms/{room}/revoke {"user_id"}` (flag `-dev`) → store xoá + hub kick mọi connection của user khỏi room, gửi `{"type":"removed","room_id"}` | Nếu không có bước này, check lúc join thành "authorize 1 lần dùng mãi" |
| Room không tồn tại vs không có quyền? | Cả hai trả **`FORBIDDEN`** | Không để lộ room nào tồn tại (enumeration) |
| Store lỗi (timeout)? | `error INTERNAL_ERROR`, **deny** (fail closed) | Không bao giờ cho qua khi không chắc |
| Timeout gọi store | `context.WithTimeout(ctx, 2s)` | readPump không treo |
| Room lazy (phase 3) còn không? | Bỏ — chỉ room có trong store mới join được | Authorization thay thế validation lỏng của phase 3 |

---

## Thay đổi code

### 1. `internal/membership/store.go`

```go
type Store interface {
	IsMember(ctx context.Context, userID, roomID string) (bool, error)
}

type InMemoryStore struct {
	mu      sync.RWMutex
	members map[string]map[string]struct{}
}

func LoadFromFile(path string) (*InMemoryStore, error)
func (s *InMemoryStore) IsMember(ctx context.Context, userID, roomID string) (bool, error)
func (s *InMemoryStore) Revoke(userID, roomID string)
```

Ở đây dùng **`RWMutex`** (không phải channel) — đối chiếu với Hub: state đơn giản, đọc rất nhiều, không có logic gọi ra ngoài lúc giữ lock → mutex hợp lý hơn.

`data/memberships.json`:

```json
{
  "restaurant-001": ["user-a", "user-b"],
  "restaurant-002": ["user-b"],
  "restaurant-003": ["user-a", "user-c"]
}
```

### 2. `Client.readPump`

```text
case join_room:
  ok, err := store.IsMember(ctx, c.userID, roomID)
  err  → INTERNAL_ERROR
  !ok  → FORBIDDEN
  ok   → hub.commands <- join
```

Race cần ý thức: giữa lúc check `IsMember` và lúc hub xử lý `join`, membership có thể bị revoke. Xử lý: lệnh `revoke` cũng đi qua hub → hub xử lý tuần tự; nếu revoke tới sau join thì kick; tới trước join thì cửa sổ race rất nhỏ — chấp nhận và ghi lại. (Chặt chẽ hơn: hub re-check với version counter — không làm ở phase này.)

### 3. Hub

- Command mới `commandRevoke{userID, roomID}`: duyệt `rooms[roomID]`, client nào có `userID` khớp thì xoá + gửi `removed`.
- `publish` giữ check `NOT_IN_ROOM` → client chưa join (dù là member) vẫn không gửi được. Nên đổi thành `FORBIDDEN`? → **Giữ `NOT_IN_ROOM`** vì đây là lỗi trạng thái, không phải quyền.

### 4. Error code

| Code | Khi nào |
| --- | --- |
| `FORBIDDEN` | Không phải member (hoặc room không tồn tại) |
| `INTERNAL_ERROR` | Store lỗi / timeout |
| `NOT_IN_ROOM` | Gửi message vào room chưa join |

---

## Test

| Test | Kiểm tra |
| --- | --- |
| `TestMemberCanJoin` | user-a join restaurant-001 → `joined` |
| `TestNonMemberGetsForbidden` | user-a join restaurant-002 → `FORBIDDEN`, connection vẫn mở |
| `TestUnknownRoomGetsForbidden` | Giống hệt response với room không có quyền |
| `TestCannotSendToUnauthorizedRoom` | Gửi `message` vào restaurant-002 không join → bị chặn, user-b trong room **không** nhận |
| `TestStoreErrorFailsClosed` | Fake store trả lỗi → `INTERNAL_ERROR`, không join |
| `TestRevokeKicksFromRoom` | user-a trong room, revoke → nhận `removed`, message sau đó không tới |
| `TestSlowStoreDoesNotBlockOtherClients` | Fake store sleep 1s cho user-a; user-b vẫn join + chat bình thường trong thời gian đó |
| `membership` unit test | Load file, IsMember, Revoke, `-race` với đọc/ghi đồng thời |

---

## Acceptance (idea.md §12 — Authorization)

- [ ] User không join được room không có quyền
- [ ] User không gửi được message vào room không có quyền
- [ ] Authorization kiểm tra ở server

---

## Điểm cần tự giải thích được

1. Authentication vs Authorization (câu 28): "bạn là ai" vs "bạn được làm gì"; valid JWT không có nghĩa là được vào mọi room.
2. Vì sao phải ở server (câu 30): client ẩn nút "Join" chỉ là UX, attacker gửi frame JSON trực tiếp.
3. Vì sao không gọi store trong hub goroutine.
4. Fail open vs fail closed.
5. Authorize lúc join vs mỗi message — trade-off chi phí vs độ tươi; revoke là cách bù.
6. Khi nào chọn `RWMutex` (store) và khi nào chọn owner goroutine (hub) — câu 17, 18.
