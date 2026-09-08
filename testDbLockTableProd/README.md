# Table Lock trên PostgreSQL Production

Ghi chú này nhìn theo góc production PostgreSQL: thao tác nào lấy lock gì, có
block đọc/ghi không, và cách làm an toàn hơn cho từng loại thao tác. Kèm theo
là một lab nhỏ bằng Go (`cmd/`) để tái hiện lock thật trên máy local thay vì
chỉ đọc lý thuyết.

## 1. Backup 1 table có lock không?

Có, nhưng nhẹ.

```bash
pg_dump -t orders dbname > orders.sql
```

PostgreSQL lấy `ACCESS SHARE` lock trên table. Lock này:

- Không block `SELECT`, `INSERT`, `UPDATE`, `DELETE`.
- Nhưng block các lệnh cần `ACCESS EXCLUSIVE` lock, ví dụ:

```sql
ALTER TABLE orders ADD COLUMN ...;
DROP TABLE orders;
TRUNCATE orders;
```

Backup table thường không làm nghẽn traffic app, nhưng có thể làm
migration/schema change phải chờ. Theo PostgreSQL docs, `ALTER TABLE` mặc
định lấy lock mạnh nhất cần thiết cho thao tác đó, và nhiều dạng lấy
`ACCESS EXCLUSIVE`.

## 2. Thêm cột / xoá cột có lock không?

### Thêm cột nullable, không default

Có, nhưng thường rất nhanh:

```sql
ALTER TABLE users ADD COLUMN nickname text;
```

Lệnh này lấy `ACCESS EXCLUSIVE` lock — trong khoảnh khắc đó block cả
read/write. Nhưng vì chỉ đổi metadata, không rewrite toàn bảng, nên thường
chỉ mất vài ms nếu không bị kẹt sau một transaction đang giữ lock trên table.

Điểm nguy hiểm: nếu có transaction khác đang giữ lock trên table, `ALTER
TABLE` sẽ phải chờ, và trong lúc chờ, các query mới phía sau cũng có thể bị
xếp hàng theo (queue) — vì PostgreSQL cấp lock theo đúng thứ tự yêu cầu.

Production nên dùng:

```sql
SET lock_timeout = '2s';
ALTER TABLE users ADD COLUMN nickname text;
```

`lock_timeout` khiến lệnh fail nhanh thay vì treo vô thời hạn và chặn cả
hàng đợi phía sau nó.

### Thêm cột `NOT NULL DEFAULT ...` — ranh giới volatile

Từ PostgreSQL 11, nếu default là biểu thức **non-volatile**, PostgreSQL lưu
giá trị đó vào `pg_attribute.attmissingval` và trả về nó khi đọc row cũ —
**không rewrite bảng**, nhanh như thêm cột nullable:

```sql
ALTER TABLE users ADD COLUMN is_priority boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
```

`now()` là stable chứ không volatile, nên vẫn chỉ được đánh giá **một lần**
rồi lưu lại — không rewrite.

Ngược lại, default **volatile** buộc PostgreSQL ghi giá trị khác nhau cho
từng row, tức **rewrite toàn bảng** và giữ `ACCESS EXCLUSIVE` suốt thời gian
đó:

```sql
ALTER TABLE users ADD COLUMN token uuid NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE users ADD COLUMN r double precision NOT NULL DEFAULT random();
```

Cũng rewrite: `GENERATED ALWAYS AS (...) STORED` và `GENERATED ... AS IDENTITY`.

Còn `ADD COLUMN ... NOT NULL` mà **không** có default thì trên bảng đã có row
sẽ lỗi thẳng (`column contains null values`) — chỉ chạy được trên bảng rỗng.

Điểm dễ nhầm: `lock_timeout` chỉ giới hạn thời gian **chờ lấy** lock, không
giới hạn thời gian **giữ** lock. Đặt `lock_timeout = '2s'` không cứu được bạn
khỏi một rewrite chạy 10 phút — muốn chặn phải dùng `statement_timeout`.

### Xoá cột

```sql
ALTER TABLE users DROP COLUMN nickname;
```

Cũng lấy `ACCESS EXCLUSIVE`, cũng block cả read lẫn write — nhưng
**không rewrite bảng**. PostgreSQL chỉ đánh dấu `attisdropped = true` trong
`pg_attribute` và đổi tên cột thành `........pg.dropped.N........`. Data cũ
vẫn nằm nguyên trong từng tuple, chỉ là không ai đọc tới nữa.

Hệ quả:

- Thời gian giữ lock vài ms, không phụ thuộc kích thước bảng.
- **Không giải phóng disk space.** Muốn lấy lại phải `VACUUM FULL` hoặc
  `pg_repack` — và `VACUUM FULL` mới là thứ nguy hiểm thật (rewrite toàn
  bảng, giữ `ACCESS EXCLUSIVE` suốt thời gian đó).
- Index/constraint phụ thuộc cột đó bị drop kèm. Nếu có FK từ bảng khác trỏ
  vào thì cần `CASCADE`, và lock lan sang bảng kia.

Rủi ro thật của `DROP COLUMN` không nằm ở bản thân câu lệnh mà ở **hàng đợi
lock**, cộng với phía app: code cũ còn `SELECT nickname` sẽ lỗi ngay khi cột
biến mất, nên phải bỏ tham chiếu trong code và deploy xong rồi mới drop cột.

## 3. Thêm data cho field nullable rồi thêm constraint thì sao?

Tùy loại constraint.

### Trường hợp nguy hiểm: thêm NOT NULL trực tiếp

```sql
ALTER TABLE users ALTER COLUMN nickname SET NOT NULL;
```

PostgreSQL phải quét toàn bảng để chắc chắn không còn NULL. Với bảng lớn,
thao tác này giữ `ACCESS EXCLUSIVE` lock trong suốt thời gian scan — có thể
là vài giây tới vài phút.

Cách an toàn hơn — tách thành 3 bước nhỏ:

```sql
-- 1. Thêm constraint nhanh, chưa validate ngay (không scan bảng)
ALTER TABLE users
  ADD CONSTRAINT users_nickname_not_null
  CHECK (nickname IS NOT NULL) NOT VALID;

-- 2. Validate riêng — vẫn scan bảng, nhưng chỉ lấy lock nhẹ (SHARE UPDATE EXCLUSIVE),
--    không chặn read/write bình thường
ALTER TABLE users VALIDATE CONSTRAINT users_nickname_not_null;

-- 3. SET NOT NULL giờ chạy gần như tức thời, vì planner đã biết chắc
--    không còn NULL nhờ constraint đã validate ở bước 2
ALTER TABLE users ALTER COLUMN nickname SET NOT NULL;
```

### Trường hợp unique

Không nên làm thẳng trên bảng lớn:

```sql
ALTER TABLE users ADD CONSTRAINT users_email_unique UNIQUE (email);
```

Nên tách ra dùng `CONCURRENTLY`:

```sql
CREATE UNIQUE INDEX CONCURRENTLY idx_users_email_unique ON users(email);

ALTER TABLE users
  ADD CONSTRAINT users_email_unique
  UNIQUE USING INDEX idx_users_email_unique;
```

## 4. Copy 1 table có lock không?

Có nhiều cách tùy mục đích.

**Copy schema + data** (đọc toàn bảng gốc, lock nhẹ `ACCESS SHARE`, nhưng tốn
I/O/CPU/WAL):

```sql
CREATE TABLE users_backup AS SELECT * FROM users;
```

**Copy chỉ schema** (nhanh hơn nhiều vì không copy data):

```sql
CREATE TABLE users_backup AS SELECT * FROM users WHERE false;
-- hoặc
CREATE TABLE users_backup (LIKE users INCLUDING ALL);
```

## 5. Thêm index có lock không?

Có.

**`CREATE INDEX` thường** — lấy `SHARE` lock, vẫn cho `SELECT` chạy nhưng
**block `INSERT`/`UPDATE`/`DELETE`** trong suốt thời gian build index. Với
bảng lớn trên production: rủi ro cao.

```sql
CREATE INDEX idx_users_email ON users(email);
```

**`CREATE INDEX CONCURRENTLY`** — lấy `SHARE UPDATE EXCLUSIVE` lock, không
chặn read/write bình thường, nên production luôn ưu tiên cách này:

```sql
CREATE INDEX CONCURRENTLY idx_users_email ON users(email);
```

Lưu ý:

- Không chạy được trong transaction block (`BEGIN ... COMMIT`) — sẽ lỗi.
- Chạy lâu hơn và tốn tài nguyên hơn bản thường.
- Nếu fail giữa chừng, có thể để lại index ở trạng thái `INVALID`, cần
  `DROP INDEX` rồi tạo lại.

Nên luôn set timeout khi chạy DDL trên production:

```sql
SET lock_timeout = '2s';
SET statement_timeout = '30min';

CREATE INDEX CONCURRENTLY idx_users_email ON users(email);
```

## 6. Các loại lock trên PostgreSQL và độ mạnh

PostgreSQL có **8 table-level lock mode**. Xếp từ nhẹ tới mạnh:

| # | Lock mode | Lệnh nào lấy | Chặn gì |
|---|---|---|---|
| 1 | `ACCESS SHARE` | `SELECT`, `COPY TO` | Chỉ xung đột với `ACCESS EXCLUSIVE` |
| 2 | `ROW SHARE` | `SELECT ... FOR UPDATE/FOR SHARE/FOR NO KEY UPDATE/FOR KEY SHARE` | `EXCLUSIVE` trở lên |
| 3 | `ROW EXCLUSIVE` | `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `COPY FROM` | `SHARE` trở lên |
| 4 | `SHARE UPDATE EXCLUSIVE` | `VACUUM` (không `FULL`), `ANALYZE`, `CREATE INDEX CONCURRENTLY`, `REINDEX CONCURRENTLY`, `ALTER TABLE ... VALIDATE CONSTRAINT`, `ALTER TABLE ... SET STATISTICS`, `COMMENT ON` | Không chặn read/write thường |
| 5 | `SHARE` | `CREATE INDEX` (không `CONCURRENTLY`) | Chặn write, cho read |
| 6 | `SHARE ROW EXCLUSIVE` | `CREATE TRIGGER`, `ALTER TABLE ... ADD FOREIGN KEY` | Chặn write, cho read |
| 7 | `EXCLUSIVE` | `REFRESH MATERIALIZED VIEW CONCURRENTLY` | Chặn tất cả trừ `SELECT` thường |
| 8 | `ACCESS EXCLUSIVE` | `DROP TABLE`, `TRUNCATE`, `VACUUM FULL`, `CLUSTER`, `REINDEX`, `REFRESH MATERIALIZED VIEW`, hầu hết `ALTER TABLE`, `LOCK TABLE` mặc định | Chặn tất cả, kể cả `SELECT` |

### Ma trận xung đột

"Mạnh/nhẹ" chỉ là cách nói tắt. Thứ thật sự quyết định "cái gì block cái gì"
là ma trận này — `✗` nghĩa là phải chờ:

| Muốn lấy ↓ \ Đang giữ → | AS | RS | RE | SUE | S | SRE | E | AE |
|---|---|---|---|---|---|---|---|---|
| `ACCESS SHARE` (AS) | | | | | | | | ✗ |
| `ROW SHARE` (RS) | | | | | | | ✗ | ✗ |
| `ROW EXCLUSIVE` (RE) | | | | | ✗ | ✗ | ✗ | ✗ |
| `SHARE UPDATE EXCLUSIVE` (SUE) | | | | ✗ | ✗ | ✗ | ✗ | ✗ |
| `SHARE` (S) | | | ✗ | ✗ | | ✗ | ✗ | ✗ |
| `SHARE ROW EXCLUSIVE` (SRE) | | | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `EXCLUSIVE` (E) | | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| `ACCESS EXCLUSIVE` (AE) | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |

Ma trận đối xứng. Bảng trên đã được kiểm chứng bằng cách chạy thật cả 64 cặp
`LOCK TABLE ... IN <mode> MODE` trên PostgreSQL 16 với `lock_timeout` ngắn,
chứ không chép từ docs.

Đọc ra được mấy điều đáng nhớ:

- `ACCESS SHARE` (đọc) chỉ bị chặn bởi đúng một thứ: `ACCESS EXCLUSIVE`. Nên
  chỉ có nhóm DDL nặng mới làm `SELECT` đứng hình.
- `SHARE` **không** tự xung đột với `SHARE` — hai `CREATE INDEX` trên cùng
  bảng chạy song song được. Nhưng nó xung đột với `ROW EXCLUSIVE`, nên chặn
  hết `INSERT`/`UPDATE`/`DELETE`.
- `SHARE UPDATE EXCLUSIVE` **tự xung đột với chính nó**. Đó là lý do hai
  `CREATE INDEX CONCURRENTLY` trên cùng một bảng phải xếp hàng chờ nhau, và
  `VACUUM` cũng không chạy song song trên cùng bảng — dù cả hai đều "không
  chặn read/write".
- `ACCESS EXCLUSIVE` là mode duy nhất chặn cả `ACCESS SHARE`, tức mode duy
  nhất làm `SELECT` phải chờ.

### Tên lock dễ gây hiểu nhầm

- `ROW SHARE` và `ROW EXCLUSIVE` là lock **cấp bảng**, không phải cấp row.
  Tên chỉ có nghĩa "tôi đang định đụng tới row trong bảng này".
- `SHARE` không nhẹ hơn `ROW EXCLUSIVE` theo kiểu trực giác — nó chặn write,
  còn `ROW EXCLUSIVE` thì không.
- Chữ `EXCLUSIVE` xuất hiện trong 5/8 tên nhưng độ mạnh rất khác nhau:
  `SHARE UPDATE EXCLUSIVE` là một trong những mode nhẹ nhất, còn
  `ACCESS EXCLUSIVE` là mạnh nhất.

### Lock của `REFRESH MATERIALIZED VIEW` nằm trên đâu?

Đây là chỗ hay nhầm nhất. **`REFRESH MATERIALIZED VIEW` lock chính
materialized view, không phải lock các bảng nguồn bằng `ACCESS EXCLUSIVE`.**

Tạo matview để tự thử:

```sql
CREATE MATERIALIZED VIEW mv_lock_test AS
  SELECT status, count(*) AS n, avg(amount) AS avg_amount
  FROM lock_test_orders GROUP BY status;

CREATE UNIQUE INDEX mv_lock_test_status ON mv_lock_test(status);
```

Giữ refresh trong một transaction mở rồi soi `pg_locks` (PostgreSQL 16):

`REFRESH MATERIALIZED VIEW mv_lock_test` — không `CONCURRENTLY`:

| Relation | Lock mode |
|---|---|
| `lock_test_orders` (bảng nguồn) | `AccessShareLock` |
| `lock_test_orders_pkey` | `AccessShareLock` |
| `mv_lock_test` | **`AccessExclusiveLock`** |
| `mv_lock_test_status` | `AccessExclusiveLock` |

`REFRESH MATERIALIZED VIEW CONCURRENTLY mv_lock_test`:

| Relation | Lock mode |
|---|---|
| `lock_test_orders` (bảng nguồn) | `AccessShareLock` |
| `lock_test_orders_pkey` | `AccessShareLock` |
| `mv_lock_test` | **`ExclusiveLock`** + `RowExclusiveLock` |
| `mv_lock_test_status` | `AccessShareLock` + `RowExclusiveLock` |

Bảng nguồn nhận đúng `ACCESS SHARE` ở **cả hai** biến thể — tức là y hệt một
câu `SELECT` bình thường. Toàn bộ phần lock mạnh nằm trên matview và index
của nó.

### Hệ quả đo được

Giữ mỗi biến thể refresh chạy trong transaction mở, rồi thử 4 thao tác từ
session khác với `lock_timeout` ngắn:

| Thao tác từ session khác | `REFRESH` thường | `REFRESH CONCURRENTLY` |
|---|---|---|
| `SELECT` trên **matview** | **BỊ CHẶN** | Chạy được (đọc dữ liệu cũ) |
| `SELECT` trên bảng nguồn | Chạy được | Chạy được |
| `INSERT` vào bảng nguồn | Chạy được | Chạy được |
| `ALTER TABLE` bảng nguồn | **BỊ CHẶN** | **BỊ CHẶN** |

Nói gọn nếu bị hỏi phỏng vấn:

> Với `REFRESH MATERIALIZED VIEW` bình thường, PostgreSQL lấy `ACCESS
> EXCLUSIVE` lock trên materialized view, nên các query đọc materialized view
> sẽ bị block trong lúc refresh. Với `CONCURRENTLY`, PostgreSQL refresh theo
> hướng concurrent để các transaction khác vẫn có thể đọc dữ liệu cũ trong
> quá trình refresh. Lock ở đây vẫn liên quan đến materialized view, không
> phải `ACCESS EXCLUSIVE` lock trên các bảng source.

### Cái bẫy: `CONCURRENTLY` không cứu được DDL trên bảng nguồn

Dòng cuối bảng trên là chỗ dễ bỏ sót: `ALTER TABLE` trên **bảng nguồn** bị
chặn ở **cả hai** biến thể. Lý do nằm ngay trong ma trận xung đột phía trên —
refresh giữ `ACCESS SHARE` trên bảng nguồn suốt thời gian chạy, mà
`ACCESS SHARE` xung đột với `ACCESS EXCLUSIVE`.

Nên một refresh chạy 30 phút sẽ làm migration xếp hàng đúng 30 phút, và mọi
query tới sau migration đó cũng xếp hàng theo. `CONCURRENTLY` bảo vệ người
đọc matview, nó không bảo vệ DDL trên bảng nguồn.

### Giá phải trả của `CONCURRENTLY`

`RowExclusiveLock` trong bảng lock ở trên đã lộ ra cách nó hoạt động: thay vì
ghi đè toàn bộ matview, PostgreSQL build dữ liệu mới vào bảng tạm rồi **apply
diff bằng `INSERT`/`UPDATE`/`DELETE`** lên matview cũ. Hệ quả:

- Chậm hơn bản thường và tốn WAL hơn.
- Tạo dead tuple, dẫn tới bloat trên matview — cần autovacuum theo kịp.
- Matview bắt buộc phải có ít nhất một `UNIQUE` index không kèm `WHERE`, nếu
  không sẽ lỗi: `cannot refresh materialized view ... concurrently`.
- Matview phải đã được populate ít nhất một lần, nếu không sẽ lỗi:
  `CONCURRENTLY cannot be used when the materialized view is not populated`.
- `EXCLUSIVE` tự xung đột với chính nó, nên hai `REFRESH ... CONCURRENTLY`
  trên cùng một matview phải chờ nhau.

Một khác biệt đáng nhớ so với `CREATE INDEX CONCURRENTLY`:
`REFRESH MATERIALIZED VIEW CONCURRENTLY` **chạy được bên trong transaction
block**.

### Row-level lock

Khác hẳn nhóm trên, đây là lock trên từng row, xếp từ nhẹ tới mạnh:

`FOR KEY SHARE` < `FOR SHARE` < `FOR NO KEY UPDATE` < `FOR UPDATE`

- `UPDATE` tự lấy `FOR NO KEY UPDATE` (hoặc `FOR UPDATE` nếu đụng cột key),
  `DELETE` lấy `FOR UPDATE`.
- Row lock **không bao giờ chặn người đọc** — nhờ MVCC, `SELECT` thường luôn
  đọc được snapshot cũ. Chỉ writer mới chờ writer.
- Nhưng transaction giữ row lock vẫn đồng thời giữ `ROW EXCLUSIVE` ở cấp
  bảng — và chính lock cấp bảng đó mới là thứ chặn DDL. Đây đúng là kịch bản
  `cmd/longtx` trong lab tái hiện.

### Điểm quan trọng nhất về vòng đời lock

**Mọi table-level lock đều được giữ tới hết transaction**, chỉ nhả khi
`COMMIT` hoặc `ROLLBACK` — không nhả sớm ngay sau khi câu lệnh chạy xong.
Nên một transaction mở lâu, dù chỉ `SELECT` một dòng, vẫn đủ để chặn đứng
migration phía sau và kéo theo cả hàng đợi.

Tự kiểm tra mode nào đang được giữ:

```sql
SELECT pid, mode, granted, relation::regclass AS table
FROM pg_locks
WHERE locktype = 'relation' AND relation = 'lock_test_orders'::regclass;

SELECT pid, pg_blocking_pids(pid), query
FROM pg_stat_activity
WHERE cardinality(pg_blocking_pids(pid)) > 0;
```

Muốn lấy lock bằng tay để thử nghiệm:

```sql
BEGIN;
LOCK TABLE lock_test_orders IN ACCESS EXCLUSIVE MODE;
-- mở session khác chạy SELECT để thấy nó đứng im
ROLLBACK;
```

## 7. Bảng tổng hợp rủi ro lock theo thao tác

| Thao tác | Lock risk | Vì sao |
|---|---|---|
| `pg_dump -t table` | Thấp | Chỉ `ACCESS SHARE` |
| `ADD COLUMN` nullable, không default | Thấp, `ACCESS EXCLUSIVE` rất ngắn | Chỉ đổi metadata |
| `ADD COLUMN NOT NULL DEFAULT <non-volatile>` | Thấp từ PostgreSQL 11 | Default lưu ở `attmissingval`, không rewrite |
| `ADD COLUMN NOT NULL DEFAULT <volatile>` | Rất cao | Rewrite toàn bảng, giữ `ACCESS EXCLUSIVE` suốt thời gian đó |
| `ADD COLUMN ... UNIQUE` / `PRIMARY KEY` | Cao | Phải build index trong lúc giữ lock |
| `ALTER COLUMN ... TYPE` | Cao | Thường phải rewrite toàn bảng |
| `SET NOT NULL` trực tiếp trên bảng lớn | Cao | Phải scan toàn bảng để đảm bảo không còn NULL |
| `ADD CHECK ... NOT VALID` | Thấp | Không scan ngay |
| `VALIDATE CONSTRAINT` | Trung bình | Có scan bảng, nhưng lock nhẹ hơn |
| `ADD UNIQUE`/`PRIMARY KEY` trực tiếp | Cao | Phải build index, có thể block write |
| `CREATE INDEX` | Cao | Block `INSERT`/`UPDATE`/`DELETE` |
| `CREATE INDEX CONCURRENTLY` | Thấp | Không chặn read/write bình thường |
| `CREATE TABLE AS SELECT` | Thấp về lock, nặng I/O | Chỉ đọc, nhưng tốn tài nguyên |
| `DROP COLUMN` | Thấp, `ACCESS EXCLUSIVE` rất ngắn | Chỉ đánh dấu `attisdropped`, không rewrite, không giải phóng disk |
| `DROP TABLE` / `TRUNCATE` | Cao | `ACCESS EXCLUSIVE`, phá cả dependency |
| `VACUUM FULL` / `CLUSTER` | Rất cao | Rewrite toàn bảng |
| `REINDEX` thường | Cao | Có thể block read/write |
| `REFRESH MATERIALIZED VIEW` không `CONCURRENTLY` | Cao trên matview, thấp trên bảng nguồn | `ACCESS EXCLUSIVE` trên matview (block đọc view); bảng nguồn chỉ `ACCESS SHARE` |
| `REFRESH MATERIALIZED VIEW CONCURRENTLY` | Thấp trên matview, thấp trên bảng nguồn | `EXCLUSIVE` trên matview, vẫn cho đọc dữ liệu cũ; cần `UNIQUE` index, chậm hơn và gây bloat |
| `UPDATE`/`DELETE` số lượng lớn | Trung bình–cao | Giữ row lock lâu, tạo bloat, tốn WAL/I/O |
| `ADD FOREIGN KEY` trực tiếp | Trung bình–cao | Cần validate dữ liệu, có thể scan bảng |
| `RENAME COLUMN`/`TABLE` | Thấp | Nhanh, lock mạnh nhưng rất ngắn |

**Rule dễ nhớ:**

- Đọc/copy/backup thường lock nhẹ nhưng tốn tài nguyên (I/O, CPU, WAL).
- DDL thường lock mạnh (`ACCESS EXCLUSIVE`) dù chạy nhanh về mặt metadata.
- Scan/rewrite bảng lớn là nguy hiểm nhất — luôn hỏi "thao tác này có phải
  quét/viết lại toàn bảng không?" trước khi chạy trên production.
- Index trên production luôn dùng `CONCURRENTLY`.
- Constraint trên production luôn theo pattern `NOT VALID` rồi
  `VALIDATE CONSTRAINT`.
- Luôn set `lock_timeout` khi chạy DDL, để lệnh fail nhanh và rõ ràng thay vì
  treo và chặn cả hàng đợi phía sau. Nhưng nhớ `lock_timeout` chỉ chặn thời
  gian **chờ** lock — thời gian **giữ** lock phải chặn bằng
  `statement_timeout`.

## 8. Lab: tái hiện lock table thật trên local (Go)

Phần trên là lý thuyết — phần này là một bảng ~2 triệu dòng chạy trong
Docker, để bạn tự tay chạy các thao tác nguy hiểm ở trên và **thấy lock xảy
ra thật**, thay vì chỉ tin vào bảng risk ở trên.

### Cấu trúc lab

```
docker-compose.yml   # PostgreSQL 16, bật log_lock_waits
go.mod
internal/db/         # config kết nối dùng chung (pgx), đọc từ env var
cmd/
  seed/                    # seed bảng lock_test_orders với ~2 triệu row
                           # (gofakeit + pgx.CopyFrom, ~3s cho 2 triệu row)
  longtx/                  # "session A": giữ transaction mở lâu
  createindex/             # CREATE INDEX thường (blocking)
  createindexconcurrently/ # CREATE INDEX CONCURRENTLY (không block)
  addcolumn/               # ADD COLUMN NOT NULL DEFAULT false (metadata-only)
  addcolumnvolatile/       # ADD COLUMN NOT NULL DEFAULT gen_random_uuid() (rewrite)
  dropcolumn/              # DROP COLUMN (metadata-only, không giải phóng disk)
  watchlocks/              # theo dõi ai đang block ai theo thời gian thực
```

Driver Postgres dùng `github.com/jackc/pgx/v5`, data giả dùng
`github.com/brianvoe/gofakeit/v7` — tương đương faker.js bên Node
(nhiều generator, không cần gọi mạng). Seed dùng `pgx.CopyFrom` (giao thức
`COPY`) thay vì insert nhiều dòng/lần, nên nhanh hơn hẳn so với cách
`INSERT ... SELECT unnest(...)`.

### Chạy lab

```bash
docker-compose up -d   # khởi động Postgres ở localhost:5432 (user/pass/db: app/app/lock_lab)
go run ./cmd/seed      # tạo bảng lock_test_orders + insert 2,000,000 rows (~3s)
```

### Tái hiện lock: `CREATE INDEX` bị chặn bởi 1 transaction đang mở

Mở 2 terminal.

Terminal 1 — mô phỏng một transaction "quên" COMMIT (giống 1 request chậm
trên production):

```bash
go run ./cmd/longtx   # giữ transaction mở 30s (đổi bằng HOLD_SECONDS=...)
```

Terminal 2 — trong lúc terminal 1 còn đang chạy:

```bash
go run ./cmd/createindex
```

Bạn sẽ thấy `createindex` đứng im, chờ đúng tới khi terminal 1 `COMMIT` thì
mới chạy tiếp — đây chính là `SHARE` lock của `CREATE INDEX` xếp hàng phía
sau `ROW EXCLUSIVE` lock mà transaction ở terminal 1 đang giữ.

Chạy lại y hệt kịch bản trên nhưng thay `createindex` bằng
`createindexconcurrently` để thấy sự khác biệt: nó vẫn phải **chờ**
transaction cũ kết thúc (để đảm bảo snapshot nhất quán), nhưng trong lúc chờ,
**write bình thường từ session khác không hề bị chặn** — khác hẳn với bản
`CREATE INDEX` thường, vốn chặn cả `INSERT`/`UPDATE`/`DELETE` của mọi
session khác.

Cũng có thể thử `go run ./cmd/addcolumn` để thấy `ALTER TABLE ... ADD
COLUMN ... NOT NULL DEFAULT ...` xếp hàng chờ tương tự — vì nó cần
`ACCESS EXCLUSIVE`, lock mạnh nhất, xung đột với mọi lock khác kể cả
`ACCESS SHARE`.

### Tái hiện: metadata-only vs rewrite toàn bảng

Ba demo dưới đây đều in `pg_relation_filenode` trước và sau khi chạy DDL.
Filenode **đổi** nghĩa là PostgreSQL đã ghi lại toàn bộ bảng vào file mới —
bằng chứng dứt điểm của rewrite, không phải suy đoán từ thời gian chạy.

```bash
go run ./cmd/addcolumn          # ADD COLUMN NOT NULL DEFAULT false
go run ./cmd/addcolumnvolatile  # ADD COLUMN NOT NULL DEFAULT gen_random_uuid()
go run ./cmd/dropcolumn         # DROP COLUMN
```

Kết quả thật trên bảng 2 triệu row (PostgreSQL 16):

| Demo | Filenode | Size | Thời gian |
|---|---|---|---|
| `addcolumn` (`DEFAULT false`) | 16506 → 16506 | 235 MB → 235 MB | 0.0s |
| `addcolumnvolatile` (`gen_random_uuid()`) | 16506 → **16518** | 235 MB → **282 MB** | 2.3s |
| `dropcolumn` | 16506 → 16506 | 235 MB → 235 MB | 0.0s |

Đọc ra ba điều:

- `DEFAULT false` là non-volatile nên **không rewrite** — nhanh bất kể bảng
  to cỡ nào. Bảng risk ở mục 7 hay bị hiểu nhầm chỗ này.
- `gen_random_uuid()` là volatile nên **rewrite toàn bảng**, filenode đổi và
  size tăng. 2.3s ở đây là với 2 triệu row trên máy local — trên bảng
  production trăm triệu row thì đây là downtime thật, vì `ACCESS EXCLUSIVE`
  bị giữ suốt thời gian rewrite.
- `DROP COLUMN` nhanh nhưng **size không giảm chút nào** — data cũ vẫn nằm
  trong từng tuple cho tới khi `VACUUM FULL`/`pg_repack`.

Chạy `./cmd/addcolumnvolatile` song song với `./cmd/watchlocks` để thấy trong
2.3s đó nó chặn mọi session khác, kể cả `SELECT`.

### Quan sát blocking chain theo thời gian thực

Mở thêm 1 terminal, chạy song song với 2 terminal trên:

```bash
go run ./cmd/watchlocks
```

Lệnh này poll `pg_locks` + `pg_stat_activity` mỗi giây và in ra PID nào
đang bị PID nào chặn, cùng câu query tương ứng — đúng những gì bạn sẽ dùng
để debug incident lock thật trên production.

### Dọn dẹp

```bash
docker-compose down -v   # xoá container + volume, làm sạch hoàn toàn
```
