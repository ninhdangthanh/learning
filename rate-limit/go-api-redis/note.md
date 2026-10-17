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

---

# PHẦN TRẢ LỜI

## Q: "Em implement rate limit ở đoạn nào trong code?"

Trả lời "ở middleware" là hụt — middleware chỉ là chỗ *thi hành*, không phải chỗ *thuật toán*. Tách 3 lớp:

**1. Lớp đấu dây — `internal/api/router.go:41-45`**
5 limiter riêng, mỗi endpoint một policy: `/auth/register` fixed window, `/auth/login` và `/auth/refresh` mỗi cái một token bucket riêng, `/api/*` sliding window, `POST /api/notes` thêm bucket ghi.

Điểm ghi thêm: login và refresh **cố ý tách bucket** dù cùng config. Nếu dùng chung, kẻ spam login sai mật khẩu làm cạn bucket sẽ khoá luôn khả năng refresh của user hợp lệ cùng IP — brute-force biến thành DoS.

**2. Lớp thi hành — `internal/ratelimit/middleware.go`**
Adapter `net/http` thuần, chỉ ~4 dòng logic: lấy key qua `KeyFunc` (`keyByIP` / `keyByIdentity`, router.go:27-36) → gọi `limiter.Allow` → set header `RateLimit-*` + `Retry-After` → chặn thì 429.
Quyết định thiết kế ở dòng 19-24: **fail-open** khi Redis lỗi, log rồi cho qua. Rate limit là lớp bảo vệ chứ không phải lớp xác thực, không nên tự biến thành SPOF.

**3. Lớp thuật toán — `internal/ratelimit/lua/*.lua`** ← câu trả lời thật
3 Lua script chạy **bên trong Redis**. Go chỉ nhúng bằng `//go:embed`, truyền `KEYS`/`ARGV`, đọc về `{allowed, remaining, reset, retry_after}`.
Lý do dùng Lua: đọc-tính-ghi phải **atomic**. Làm `GET` → so sánh → `INCR` từ Go thì 2 request song song cùng đọc giá trị cũ và cùng cho qua. Lua chạy trong Redis nên cả khối là một thao tác nguyên tử, lại chỉ tốn **1 round-trip** thay vì 3.

**Bản 20 giây:**
> Ba lớp. Thuật toán thật nằm trong Lua script chạy trong Redis — `internal/ratelimit/lua/`, 3 file cho fixed window, sliding window counter, token bucket. Mỗi limiter Go là wrapper mỏng bọc script đó qua interface `Limiter` một method `Allow`. Middleware `net/http` chỉ lấy key, gọi `Allow`, set header, trả 429. Chọn endpoint nào dùng thuật toán nào thì ở router. Em để logic trong Lua vì cần atomic giữa nhiều instance — làm từ Go sẽ có race.

**3 câu hỏi nối tiếp gần như chắc chắn có:**
- *Interface `Limiter` để làm gì?* → `limiter.go`, một method `Allow`. Đổi thuật toán cho một endpoint chỉ sửa 1 dòng ở router; middleware không biết gì về thuật toán bên dưới; test mock được.
- *Key đặt thế nào?* → `rl:{fw|sw|tb}:{scope}:{key}`, scope là tên endpoint nên các policy không giẫm chân nhau. `keyByIdentity` ưu tiên `user:{id}`, chưa đăng nhập mới rơi về `ip:{ip}`.
- *`now` lấy từ đâu?* → truyền từ Go xuống `ARGV`, không gọi `redis.call('TIME')`. Đánh đổi: phải chấp nhận clock skew giữa các pod.

## Q: Redis single-thread rồi, sao 2 instance BE vẫn race? Sao còn cần Lua?

**Hiểu nhầm cần sửa trước:** single-threaded nghĩa là **mỗi COMMAND là atomic**, KHÔNG phải **mỗi CHUỖI command của bạn là atomic**.

Redis không bao giờ chạy 2 command cùng lúc. Nhưng nó cũng không hứa rằng 3 command của instance A sẽ chạy liền nhau — command của instance B **chen vào giữa** được. Kẽ hở nằm ở **khoảng trống giữa các command**, không nằm bên trong một command.

Timeline token bucket, capacity = 1, hai instance cùng lúc:

| Thứ tự Redis chạy | Command | Kết quả |
|---|---|---|
| 1 | BE-A: `HMGET key tokens` | → 1 |
| 2 | BE-B: `HMGET key tokens` | → 1 (vẫn là 1, A chưa ghi!) |
| 3 | BE-A: tính trong Go: 1 >= 1 → cho qua, `HSET tokens 0` | ghi 0 |
| 4 | BE-B: tính trong Go: 1 >= 1 → cho qua, `HSET tokens 0` | ghi 0 |

→ 2 request lọt qua trong khi capacity chỉ là 1. Redis **chưa hề** chạy song song lệnh nào — nó chạy đúng 4 lệnh tuần tự. Vẫn sai.

Lý do: bước "tính toán" nằm ở **phía Go**, không nằm trong Redis. Giữa lúc A đọc và lúc A ghi có một khoảng trống, và B lọt vào đó.

**Quan trọng: không phải chuyện "2 instance".** Ngay cả 1 process Go duy nhất cũng dính, vì mỗi request là một goroutine riêng chạy song song. Hai goroutine trong cùng 1 pod tạo ra đúng timeline trên. "Nhiều instance" chỉ làm nó xảy ra thường xuyên hơn và làm mất luôn lựa chọn dùng mutex in-memory.

**Lua sửa cái gì:** `EVAL` là **một command**. Cả script — đọc, tính, so sánh, ghi — với Redis chỉ là 1 lệnh duy nhất. Không còn khoảng trống nào để chen vào. Timeline thành: A chạy trọn script (tokens 1→0), rồi B chạy trọn script (thấy tokens=0 → chặn). Đúng.

**Nói chính xác thì từng thuật toán cần Lua ở mức khác nhau** (nói được cái này là ăn điểm vì cho thấy hiểu bản chất chứ không học vẹt):
- **Fixed window** — gần như KHÔNG cần Lua. `INCR` vốn đã atomic và trả về giá trị mới, so sánh với limit ở Go vẫn đúng. Lua ở đây chỉ để gộp `INCR` + `PEXPIRE` vào 1 round-trip và tránh tình huống `INCR` xong thì process chết trước khi kịp `EXPIRE` → key sống vĩnh viễn.
- **Sliding window counter** — BẮT BUỘC. Phải đọc 2 counter, nhân trọng số, so sánh, rồi mới quyết định có `INCR` hay không. Logic rẽ nhánh dựa trên giá trị vừa đọc.
- **Token bucket** — BẮT BUỘC. Đây là read-modify-write kinh điển: đọc `tokens`/`updated_at`, tính refill theo thời gian trôi, so sánh, ghi lại.

**Vì sao không dùng `MULTI`/`EXEC` thay Lua?** `MULTI` gom các lệnh chạy liền mạch thật, nhưng nó **xếp hàng trước rồi mới chạy** — bạn không đọc được giá trị ở giữa transaction để rẽ nhánh. Mà rẽ nhánh chính là toàn bộ việc của rate limiter. `WATCH` + `MULTI` (optimistic lock) thì làm được nhưng phải tự retry khi đụng độ, và trên hot key thì tỉ lệ đụng độ rất cao → retry liên tục, chậm hơn Lua nhiều.

**Bản 20 giây:**
> Single-thread đảm bảo từng command atomic, không đảm bảo chuỗi command của em atomic. Command của instance khác chen vào khoảng trống giữa các command của em được. Với token bucket em phải đọc số token, tính refill, rồi ghi lại — ba bước, hai khoảng trống. Bọc vào Lua thì cả ba bước thành một command duy nhất với Redis, hết khoảng trống. Tiện thể tiết kiệm 2 round-trip.
