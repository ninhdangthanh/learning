# Phase 4 — Authentication (JWT)

Mỗi WebSocket connection phải gắn với 1 `userID` đã xác thực. `sender_id` lấy từ token, **không bao giờ** từ payload client.

---

## Mục tiêu

```text
Client ──GET /ws  (token)──► Handler
                               │
                        verify JWT  ← TRƯỚC Upgrade
                               │
            ┌──────────────────┴───────────────────┐
         invalid / expired                       valid
            │                                      │
     HTTP 401 (không có 101)              Upgrade → 101
                                          Client{userID: claims.Subject}
```

Trả lời câu hỏi idea.md: **authentication thực hiện ở handshake HTTP, trước khi Upgrade.** Lúc đó vẫn còn trả được HTTP status (401) rõ ràng, không tốn goroutine/buffer cho kẻ lạ.

---

## Các quyết định cần chốt

| Câu hỏi | Chốt | Lý do |
| --- | --- | --- |
| Token gửi thế nào? | Ưu tiên `Authorization: Bearer <jwt>`; fallback query `?access_token=` | Browser `new WebSocket()` **không set được header** → cần query cho web client; tool/test Go dùng header |
| Rủi ro query param | Token lọt access log / proxy log → middleware log phải **redact** `access_token`; token TTL ngắn (15 phút) | Ghi rõ trong README. Production: dùng "ticket" 1 lần (xem cuối file) |
| Thuật toán | HS256, secret từ env `JWT_SECRET` (bắt buộc ≥ 32 byte, thiếu thì fail khi start) | Đủ cho project học; `jwtToken/` đã có RS256 nếu muốn đổi |
| Claims bắt buộc | `sub`, `exp`; kiểm `iss = "ws-chat"`; `WithLeeway(30s)` | Chặn token của hệ khác, chịu lệch giờ nhỏ |
| Chặn `alg=none` / đổi alg | `jwt.WithValidMethods([]string{"HS256"})` | Lỗ hổng kinh điển |
| Token hết hạn **khi đang** connect | Writer set timer tại `exp` → gửi close **4001 "token expired"** | JWT chỉ check lúc handshake thì connection sống mãi sau khi token hết hạn |
| Cấp token để test | `POST /dev/token {"sub":"user-123"}` chỉ bật khi flag `-dev` | Không cần dựng auth server riêng |
| Origin check | Giữ `CheckOrigin` mặc định (same-origin) + flag `-allowed-origins` | JWT qua query không chống được CSWSH nếu dùng cookie; vẫn nên check origin |

---

## Thay đổi code

### 1. `internal/auth/jwt.go`

```go
type Claims struct {
	jwt.RegisteredClaims
}

type Verifier struct {
	secret []byte
	issuer string
}

func NewVerifier(secret []byte, issuer string) (*Verifier, error)
func (v *Verifier) Verify(token string) (Identity, error)

type Identity struct {
	UserID    string
	ExpiresAt time.Time
}

type Issuer struct { ... }
func (i *Issuer) Issue(userID string, ttl time.Duration) (string, error)
```

Lỗi phân loại được: `ErrTokenMissing`, `ErrTokenExpired`, `ErrTokenInvalid` (dùng `errors.Is(err, jwt.ErrTokenExpired)` của v5).

### 2. `internal/ws/handler.go`

```text
ServeHTTP
  token := bearerFromHeader(r) ?: r.URL.Query().Get("access_token")
  identity, err := verifier.Verify(token)
  err → http.Error(401, "unauthorized") + header WWW-Authenticate: Bearer error="invalid_token"
  ok  → Upgrade → newClient(identity, conn, ...)
```

`Handler` phụ thuộc interface nhỏ để test dễ:

```go
type TokenVerifier interface {
	Verify(token string) (auth.Identity, error)
}
```

### 3. `Client`

- Thêm `userID string`, `expiresAt time.Time`.
- `writePump`: `expiry := time.NewTimer(time.Until(expiresAt))`, `case <-expiry.C:` → close 4001 → return.
- Logger thêm `user_id`.
- `sender_id` trong message = `c.userID`.

### 4. `cmd/server/main.go`

- Đọc `JWT_SECRET` từ env; flag `-dev` bật `POST /dev/token`.
- Access log middleware (nếu có) redact query `access_token`.

### 5. `web/index.html`

Ô nhập userID → gọi `/dev/token` → connect `ws://.../ws?access_token=...`. Hiển thị close code 4001 khi hết hạn.

---

## Test

| Test | Kiểm tra |
| --- | --- |
| `TestRejectsMissingToken` | Dial fail, `resp.StatusCode == 401` |
| `TestRejectsInvalidSignature` | Token ký bằng secret khác → 401 |
| `TestRejectsExpiredToken` | `exp` quá khứ (ngoài leeway) → 401 |
| `TestRejectsAlgNone` | Token `alg: none` → 401 |
| `TestRejectsWrongIssuer` | 401 |
| `TestAcceptsHeaderToken` / `TestAcceptsQueryToken` | 101 |
| `TestSenderIDComesFromToken` | Client gửi `{"sender_id":"hacker",...}` → message broadcast có `sender_id` = sub của token |
| `TestConnectionClosedWhenTokenExpires` | TTL 1s → nhận close 4001 |
| `internal/auth` unit test | Issue → Verify round-trip, các lỗi phân loại đúng |

---

## Acceptance (idea.md §12 — Authentication)

- [ ] JWT invalid bị reject
- [ ] JWT expired bị reject
- [ ] JWT hợp lệ tạo authenticated connection
- [ ] `userID` gắn với WebSocket connection

---

## Mở rộng (không bắt buộc) — Ticket pattern

```text
1. POST /ws-ticket  (Authorization: Bearer <access token>)  → {"ticket":"random-32B", "ttl":30}
2. new WebSocket("/ws?ticket=...")
3. Server: ticket store (map + TTL, 1 lần dùng) → userID → xoá ticket
```

Ticket bị lọt log cũng vô dụng (đã dùng / hết hạn sau 30s). Đây là cách phổ biến ở production khi client là browser.

---

## Điểm cần tự giải thích được

1. Authentication diễn ra ở đâu và vì sao trước Upgrade (câu 27).
2. Vì sao **không tin** `userID` client gửi (câu 29) — client điều khiển hoàn toàn payload.
3. Vì sao browser không gửi được `Authorization` header cho WebSocket, và các cách thay thế (query, subprotocol, cookie, ticket) + trade-off.
4. Vì sao check `exp` lúc handshake chưa đủ với connection sống lâu.
5. Cookie auth + WebSocket = nguy cơ Cross-Site WebSocket Hijacking → vai trò của `CheckOrigin`.
