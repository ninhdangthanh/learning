dùng rate limit để làm gì
rate limit, throtting, burst là gì
....
## Thuật toán rate limit

- Fixed window / sliding window counter / token bucket khác nhau ở đâu? Vì sao `/auth/register` dùng fixed window, `/auth/login` dùng token bucket, `/api/*` dùng sliding window?
- Fixed window bị "boundary burst": tại sao user có thể bắn 2x limit quanh ranh giới cửa sổ? Cách chứng minh bằng demo.sh?
- Sliding window counter trong `lua/sliding_window.lua` là ước lượng (`previous * overlap + current`). Sai số tối đa bao nhiêu? Khi nào ước lượng này cho qua/chặn nhầm?
- Sliding window log (ZSET + ZREMRANGEBYSCORE) chính xác hơn — vì sao không dùng? Đánh đổi memory/CPU là gì?
- Token bucket lưu `tokens` dạng float trong HASH nhưng trả về `math.floor(tokens)` — vì sao? Remaining hiển thị cho client có bị lệch không?
- Token bucket vs leaky bucket: cái nào cho phép burst, cái nào làm phẳng traffic?
- `RefillPerSecond: 0.2` cho login nghĩa là gì theo ngôn ngữ nghiệp vụ (1 token / 5s, burst 10)?
- Muốn tính phí khác nhau theo endpoint nặng/nhẹ (cost > 1) thì sửa ở đâu? Hiện `ARGV[4]` đang hardcode 1.
- GCRA (cell rate) khác token bucket thế nào? Vì sao nó chỉ cần 1 key thay vì hash 2 field?

## Vì sao Redis + Lua

- Vì sao logic phải nằm trong Lua script mà không phải `INCR` + `EXPIRE` từ Go? (atomic, 1 round-trip, race giữa nhiều instance)
- `redis.NewScript` chạy EVALSHA rồi fallback EVAL khi NOSCRIPT — chuyện gì xảy ra khi Redis restart hoặc `SCRIPT FLUSH`?
- Thời gian `now` được truyền từ Go (`time.Now().UnixMilli()`) chứ không lấy `redis.call('TIME')`. Ưu/nhược? Nhiều pod lệch clock thì sao?
- Script `revokeSessionsScript` gọi `DEL 'refresh:' .. member` với key không khai báo trong `KEYS[]` — chạy trên Redis Cluster sẽ hỏng thế nào?
- Nếu chuyển sang Redis Cluster, key `rl:sw:api:user:123` rơi vào slot nào? Cần hash tag `{}` ở đâu?
- Redis single node là SPOF: khi Redis chết thì middleware hiện đang **fail-open** (log rồi cho qua). Đúng hay sai? Khi nào phải fail-closed?
- Khi ElastiCache failover, counter mất sạch → user vượt limit trong chốc lát. Chấp nhận được không? Cách giảm thiểu?
- `maxmemory-policy allkeys-lru` sẽ evict cả key rate limit lẫn `user:*` (đang là DB chính). Vì sao phải tách instance / tách DB?
- Persistence RDB vs AOF ảnh hưởng gì tới rate limit và tới dữ liệu user?
- Mỗi request tốn 1 round-trip Redis: đo p99 overhead thế nào? Có nên cache cục bộ / batch / sharded counter khi QPS rất cao?

## Thiết kế key & chống lách luật

- `keyByIP` dùng `httpx.ClientIP` + chi `middleware.RealIP` (đọc X-Forwarded-For). Client tự set header này có spoof được không? Sau ALB/CloudFront thì tin header nào, tin bao nhiêu hop?
- `keyByIdentity` fallback về IP khi chưa có user — trong router `/api` đã có `RequireAuth` chạy trước nên nhánh fallback có bao giờ xảy ra?
- Thứ tự middleware: `RequireAuth` đứng TRƯỚC rate limit ở `/api`. Nghĩa là request token rác vẫn được verify JWT + hit Redis denylist mà không bị giới hạn. Có nên đảo lại không?
- IPv6: limit theo /128 hay /64? Tại sao limit từng IPv6 đơn lẻ gần như vô dụng?
- NAT/văn phòng chung IP: limit theo IP làm ảnh hưởng người dùng vô tội — xử lý thế nào?
- Attacker tạo nhiều account để né limit theo user — cần thêm tầng limit nào (per-IP, per-device, per-tenant, global)?
- Có nên có global circuit breaker / limit toàn hệ thống ngoài per-key không?

## Hợp đồng HTTP với client

- Bộ header `RateLimit-Limit / Remaining / Reset / Retry-After` theo draft IETF — client nên xử lý ra sao (exponential backoff + jitter)?
- Vì sao trả 429 chứ không phải 403? Khi nào nên dùng 503?
- `secondsCeil` ép tối thiểu 1 giây — có làm client chờ lâu hơn cần thiết không?
- Có nên lộ `RateLimit-Remaining` cho endpoint auth không? (thông tin cho attacker dò ngưỡng)

## Auth / JWT / session

- Refresh token rotation: vì sao `consumeRefreshScript` phải atomic? Hai request refresh song song cùng 1 jti sẽ ra kết quả gì?
- Reuse detection qua key `refresh:used:<jti>`: khi phát hiện reuse thì hệ thống nên revoke toàn bộ session của user chưa? Code hiện đã làm chưa?
- Denylist access token theo `jti` khiến mỗi request phải hỏi Redis — vậy còn gì là "JWT stateless"? Đánh đổi có đáng không? Thay bằng access TTL ngắn (1-2 phút) được không?
- TTL của key denylist nên bằng thời gian sống còn lại của token — nếu đặt dài hơn/ngắn hơn thì hệ quả gì?
- HS256 vs RS256/EdDSA: khi nào buộc phải đổi? Rotate secret không downtime thì cần `kid` ở đâu?
- `Parse` đã kiểm tra `alg`, `iss`, `aud`, `typ`, `exp`. Vì sao check `WithValidMethods` là bắt buộc (tấn công alg=none / HMAC-with-RSA-pubkey)?
- `CreateUser`: `SETNX email` thành công nhưng `HSET` lỗi → code `DEL` email để rollback. Nếu process chết giữa chừng thì sao? Email bị "khoá mồ côi" xử lý thế nào?
- Dùng Redis làm datastore chính cho user — rủi ro gì? Khi nào phải chuyển sang Postgres và Redis chỉ còn làm cache/limiter?
- Login với email không tồn tại trả về nhanh hơn (không chạy bcrypt) → user enumeration qua timing. Fix thế nào?
- bcrypt cost đang là bao nhiêu? Vì sao chính bcrypt tốn CPU lại là lý do bắt buộc phải rate limit `/auth/login`?

## Vận hành & test

- Test dùng `miniredis` — nó thực sự chạy Lua (gopher-lua) hay mock? Có khác biệt nào với Redis thật khiến test xanh mà prod đỏ?
- Test `TestTokenBucketRefillsOverTime` dùng `time.Sleep` — làm sao viết test thời gian mà không flaky (inject clock)?
- Thiếu test nào? (sliding window qua ranh giới cửa sổ, middleware trả 429 + header, fail-open khi Redis chết)
- Nên expose metric gì: số request bị 429 theo endpoint/key, latency của Lua script, tỉ lệ fail-open? Alert khi nào?
- Log key rate limit (chứa IP/user id) — có vướng PII không?
- So với rate limit ở tầng WAF / API Gateway / ALB / nginx / Envoy: vì sao vẫn cần làm ở tầng app? Nên đặt ở đâu là tốt nhất?
- Triển khai nhiều instance API sau load balancer: vì sao rate limit in-memory không dùng được, và Redis giải quyết đúng vấn đề gì?
- `middleware.Timeout(15s)` + Redis ReadTimeout 3s: nếu Redis treo thì request chờ bao lâu? Có cần context riêng ngắn hơn cho limiter không?
