# Go API + Redis

Bản "chạy thật" của các thuật toán rate limit trong repo này: một REST API viết bằng Go, dùng Redis làm nơi lưu **toàn bộ** state — user, note, counter rate limit. Không có database nào khác.

Một binary (`cmd/api`), một Redis.

---

## Chạy thử

```bash
cp .env.example .env
make up          # redis + api qua docker compose
make demo        # đăng ký → tạo note → đổi refresh token → chạm trần từng limiter
make load-test   # bắn 70 request để thấy sliding window chặn ở đâu
```

Chạy không cần Docker cho phần app:

```bash
make redis    # terminal 1
make api      # terminal 2
make demo     # terminal 3
```

---

## 3 thuật toán rate limit, mỗi cái một chỗ dùng

Mỗi limiter là một Lua script chạy atomic trong Redis. Lua quan trọng ở chỗ: đọc counter rồi mới ghi từ phía Go là hai lệnh tách rời, nhiều instance chạy song song sẽ ghi đè nhau. Lua script chạy như một đơn vị không bị chen ngang, nên counter luôn đúng dù có bao nhiêu pod.

| Thuật toán | Cấu trúc Redis | Gắn ở đâu | Mặc định | Key theo |
|---|---|---|---|---|
| **Fixed Window Counter** | `INCR` + `PEXPIRE` trên key gắn số hiệu window | `POST /auth/register` | 5 / giờ | IP |
| **Token Bucket** | Hash `{tokens, updated_at}`, refill theo thời gian trôi | `POST /auth/login`, `POST /auth/refresh`, `POST /api/notes` | burst 10 (auth) · burst 20 (write) | IP · user |
| **Sliding Window Counter** | 2 counter (window trước + hiện tại), nội suy theo tỉ lệ chồng lấn | toàn bộ `/api/*` | 60 / phút | user |

Vì sao chia như vậy:

* **Register** chỉ cần một quota thô và rẻ. Fixed window có nhược điểm dồn burst ở ranh giới window, nhưng với 5 request/giờ thì điều đó vô hại.
* **Login** cần cho người dùng gõ sai vài lần liên tiếp mà không bị chặn ngay, rồi siết dần. Đó đúng là hành vi token bucket: cho burst, sau đó chỉ nhả token từ từ. `POST /api/notes` dùng lại ý tưởng này cho thao tác ghi — cho phép nhập liệu dồn dập trong chốc lát, nhưng không cho spam kéo dài.
* **API thường** cần công bằng và mượt, không cho ai lách qua ranh giới window. Sliding window counter cho độ chính xác gần bằng sliding window log nhưng chỉ tốn 2 key thay vì lưu từng timestamp.

**`/auth/login` và `/auth/refresh` có bucket riêng dù cùng cấu hình.** Nếu dùng chung, kẻ tấn công spam login sai mật khẩu sẽ làm cạn bucket và khoá luôn khả năng refresh của người dùng hợp lệ trên cùng IP — brute-force biến thành DoS.

Mọi response đều kèm header chuẩn:

```
RateLimit-Policy: sliding_window_counter
RateLimit-Limit: 60
RateLimit-Remaining: 0
RateLimit-Reset: 41
Retry-After: 41
```

**Khi Redis chết, limiter fail-open** — request vẫn đi qua, lỗi được ghi log. Đây là lựa chọn có chủ đích: rate limit là lớp bảo vệ, không phải lớp xác thực, nên nó không nên tự biến thành single point of failure. Muốn fail-closed thì sửa `internal/ratelimit/middleware.go`.

---

## JWT

Cả hai token đều stateless. Redis không giữ session nào.

| Thành phần | Chi tiết |
|---|---|
| Access token | HS256, mặc định 15 phút, claims `sub` `jti` `typ` `role` `iss` `aud` `exp` `nbf` `iat` |
| Refresh token | mặc định 7 ngày, gửi lên `/auth/refresh` để đổi lấy cặp token mới |

Verify hai tầng: token phải đúng chữ ký **và** đúng `typ`. Một access token không thể đem đi refresh, và ngược lại.

**Không có thu hồi** — không rotation, không reuse detection, không denylist, không logout phía server. Đây là lựa chọn có chủ đích để repo này tập trung vào rate limit; phần token chuyên sâu nằm ở project khác. Hệ quả: token bị lộ sẽ dùng được tới khi hết hạn, và hàng rào duy nhất là TTL ngắn của access token (`ACCESS_TOKEN_TTL`). Đổi lại, `RequireAuth` chỉ verify chữ ký, không chạm Redis lần nào.

---

## API

### Auth

| Method | Path | Rate limit | Mô tả |
|---|---|---|---|
| `POST` | `/auth/register` | fixed window | Tạo tài khoản, trả về luôn cặp token |
| `POST` | `/auth/login` | token bucket | Đăng nhập |
| `POST` | `/auth/refresh` | token bucket riêng | Đổi refresh token lấy cặp token mới |
| `GET` | `/auth/me` | — | Thông tin tài khoản |

Không có `/auth/logout`: client tự xoá token của mình.

### Notes

Tất cả nằm sau `RequireAuth` và sliding window của `/api/*`.

| Method | Path | Rate limit thêm | Mô tả |
|---|---|---|---|
| `GET` | `/api/notes?offset=&limit=` | — | Danh sách, mới nhất trước |
| `POST` | `/api/notes` | token bucket | Tạo note |
| `GET` | `/api/notes/{id}` | — | Chi tiết |
| `PATCH` | `/api/notes/{id}` | — | Sửa title và/hoặc body |
| `DELETE` | `/api/notes/{id}` | — | Xoá |

`GET /healthz` không cần auth, ping Redis để kiểm tra kết nối.

---

## Keyspace Redis

| Key | Kiểu | Nội dung |
|---|---|---|
| `user:{id}` | Hash | email, password_hash, role, created_at |
| `user:email:{email}` | String | id, dùng `SETNX` để đảm bảo email không trùng |
| `note:{id}` | Hash | nội dung note |
| `user:{id}:notes` | ZSET | index note theo thời gian tạo |
| `rl:fw:{scope}:{key}:{window}` | String | counter fixed window |
| `rl:tb:{scope}:{key}` | Hash | state token bucket |
| `rl:sw:{scope}:{key}:{window}` | String | counter sliding window |

---

## Cấu hình

Toàn bộ đọc từ biến môi trường, xem `.env.example`.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `REDIS_ADDR` | `127.0.0.1:6379` | Địa chỉ Redis |
| `JWT_SECRET` | `dev-secret-change-me` | Tối thiểu 16 ký tự, app cảnh báo nếu còn để mặc định |
| `ACCESS_TOKEN_TTL` / `REFRESH_TOKEN_TTL` | `15m` / `168h` | Tuổi thọ token |
| `RL_REGISTER_LIMIT` / `RL_REGISTER_WINDOW` | `5` / `1h` | Fixed window cho register |
| `RL_LOGIN_CAPACITY` / `RL_LOGIN_REFILL_PER_SECOND` | `10` / `0.2` | Token bucket cho login và refresh |
| `RL_API_LIMIT` / `RL_API_WINDOW` | `60` / `1m` | Sliding window cho `/api/*` |
| `RL_WRITE_CAPACITY` / `RL_WRITE_REFILL_PER_SECOND` | `20` / `1` | Token bucket cho thao tác ghi |

---

## Test

```bash
make test
```

Dùng `miniredis` nên không cần Redis thật:

* **`internal/ratelimit`** — mỗi thuật toán chặn đúng ngưỡng, key này không ảnh hưởng key kia, token bucket hồi token theo thời gian.
* **`internal/auth`** — email trùng bị chặn, sai mật khẩu bị từ chối, refresh trả về cặp token mới, refresh token cũ vẫn dùng lại được (đúng như thiết kế không thu hồi), refresh của user đã biến mất bị từ chối, access token không dùng thay refresh được.

---

## Cấu trúc thư mục

```
cmd/api              server REST
internal/api         router, wiring middleware
internal/auth        JWT, password, user store, middleware
internal/ratelimit   3 limiter + Lua script + middleware
internal/notes       CRUD trên Redis
internal/config      đọc env
internal/httpx       JSON response, client IP, graceful server
scripts              demo.sh, load-test.sh
```
