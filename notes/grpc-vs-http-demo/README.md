# gRPC vs HTTP — cùng một CRUD, hai đường vào

Demo Go + React, **không database**, cùng một `map` in-memory được phục vụ qua ba cổng khác nhau.
Mục tiêu không phải là "gRPC nhanh hơn", mà là trả lời câu hỏi:

> **Vì sao gRPC dùng trên web lại thấy gượng gạo hơn HTTP?**

Câu trả lời ngắn nằm ở cuối file. Nhưng nên chạy demo trước, vì cái đắt nhất ở đây là *nhìn thấy*
byte thật đi qua dây.

---

## Chạy

Cần: Go 1.22+, Node 18+, [`buf`](https://buf.build) (chỉ khi muốn generate lại code).

```bash
# Terminal 1 — Go server, mở cả 3 cổng
cd server && go run .

# Terminal 2 — React
cd web && npm install && npm run dev
```

Mở http://localhost:5173.

Generate lại stub sau khi sửa `.proto`:

```bash
make gen     # hoặc: buf generate
```

---

## Ba cổng, một store

```
                      ┌──────────────────────────────┐
                      │   store.Store (map + mutex)  │
                      └───────────────┬──────────────┘
                                      │
             ┌────────────────────────┼────────────────────────┐
             │                        │                        │
        :8080 REST              :50051 gRPC             :8082 gRPC-Web
        HTTP/1.1 + JSON         HTTP/2 + protobuf       HTTP/1.1 + protobuf
             │                        │                        │
             │                        │                        │
        ┌────┴────┐              ┌────┴────┐              ┌────┴────┐
        │ browser │              │ grpcurl │              │ browser │
        │  curl   │              │ Go/Java │              └─────────┘
        │ Postman │              │  client │
        └─────────┘              └─────────┘
                                      ▲
                                 browser KHÔNG
                                 với tới được
```

Chi tiết code:

| Tầng | File |
|---|---|
| Contract | `proto/product/v1/product.proto` |
| Store in-memory | `server/store/store.go` |
| REST handlers | `server/rest/rest.go` |
| gRPC service | `server/grpcsvc/service.go` |
| 3 listener | `server/main.go` |
| Client REST | `web/src/api/rest.ts` |
| Client gRPC | `web/src/api/grpc.ts` |
| Wire log | `web/src/wire.ts` |

Cổng `:8082` là `grpcweb.WrapServer(grpcServer)` của `improbable-eng/grpc-web` — bọc **đúng** cái
`grpc.Server` ở `:50051`, chạy thuần Go nên không cần Envoy hay Docker. Trong production người ta
hay dùng Envoy hoặc Nginx cho vai trò này; bản chất vẫn là **một tầng dịch nằm giữa**.

---

## Đo thử ngay trên terminal

**REST đọc được bằng mắt, gõ tay được:**

```bash
curl http://localhost:8080/api/products
# [{"id":"p001","name":"Bàn phím cơ Keychron K2","price":2190000,"qty":12}, ...]

curl -X POST http://localhost:8080/api/products \
  -H 'Content-Type: application/json' \
  -d '{"name":"Ổ cứng Samsung T7 1TB","price":2790000,"qty":15}'

curl -i http://localhost:8080/api/products/khong-co
# HTTP/1.1 404 Not Found
```

**gRPC thì phải có công cụ riêng, và phải bật reflection thì công cụ mới biết đường:**

```bash
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext localhost:50051 product.v1.ProductService/ListProducts
grpcurl -plaintext -d '{"id":"khong-co"}' localhost:50051 product.v1.ProductService/GetProduct
# ERROR: Code: NotFound
```

Thử `curl` thẳng vào cổng gRPC xem:

```bash
curl -v http://localhost:50051
# > GET / HTTP/1.1
# * Received HTTP/0.9 when not allowed
# * Closing connection
# curl: (1) Received HTTP/0.9 when not allowed
```

Server không hề trả về một HTTP response nào — nó trả về HTTP/2 frame, và `curl` nhìn vào chỉ thấy
rác không parse nổi. Đó không phải lỗi cấu hình, đó chính là điểm mấu chốt.

---

## Số liệu thật từ demo này

Cùng một store 5 sản phẩm, đo bằng chính Wire log trong UI:

| Thao tác | REST/JSON | gRPC-Web | Chênh |
|---|---:|---:|---|
| `ListProducts` response | 369 B | 234 B | **−37%** |
| `CreateProduct` request | 61 B | 39 B | **−36%** |
| `CreateProduct` response | 74 B | 68 B | −8% |

Con số gRPC đã tính cả 5 byte framing mỗi message và cả trailer frame, nên đây là so sánh
*bất lợi* cho gRPC mà nó vẫn thắng. Lý do: JSON phải gửi kèm tên field (`"name"`, `"price"`,
`"qty"`) trong **từng** record, còn protobuf chỉ gửi số hiệu field 1 byte. Danh sách càng dài,
khoảng cách càng giãn.

Nhưng nhìn kỹ cột bên phải trong Wire log:

```
REST   response  application/json          369 B   đọc được
       [{"id":"p001","name":"Bàn phím cơ Keychron K2","price":2190000,...

gRPC   response  application/grpc-web+proto 234 B  nhị phân
       00 00 00 00 d0 0a 29 0a 04 70 30 30 31 12 1a 42 c3 a0 6e 20 …
```

Bạn vừa tiết kiệm 135 byte và đánh đổi bằng khả năng đọc.

---

## Vì sao gRPC "thiếu tự nhiên" hơn HTTP

### 1. Phải qua bước codegen mới viết được dòng logic đầu tiên

REST: mở `rest.go`, gõ một handler, xong. Thêm field vào response? Thêm một dòng struct tag.

gRPC: sửa `product.proto` → chạy `buf generate` → sinh lại **cả** `server/gen/` **lẫn**
`web/src/gen/` → lúc đó mới sửa được code. Quên generate thì compile lỗi ở chỗ chẳng liên quan gì.
Đây là cái giá phải trả trước, mỗi lần, cho contract.

Thử xoá `server/gen/` rồi `go build` xem — cả project chết. Xoá handler REST thì chỉ chết đúng route đó.

### 2. Browser không phải công dân hạng nhất của gRPC

Đây là lý do lớn nhất, và demo này có hẳn một nút để bạn tự chứng kiến: bấm
**"Thử gọi thẳng cổng gRPC thuần :50051"** → `TypeError: Failed to fetch`.

gRPC cần những thứ mà JavaScript trong browser **không có quyền chạm tới**:

- **HTTP/2 frame ở mức thấp** — gRPC cần tự điều khiển stream, còn `fetch()` chỉ cho bạn một
  abstraction request-response. Browser có nói HTTP/2, nhưng không cho JS điều khiển frame.
- **HTTP trailer** — gRPC đặt status thật (`grpc-status`, `grpc-message`) ở *trailer*, tức là header
  gửi **sau** body. Fetch API không expose trailer. Không đọc được trailer thì không biết RPC
  thành công hay thất bại.
- **Streaming hai chiều** — client streaming và bidirectional streaming đơn giản là không làm được
  trên nền tảng browser.

Nên mới phải đẻ ra gRPC-Web: một **phương ngữ khác** của gRPC, nhét trailer vào cuối body dưới dạng
một frame đặc biệt, chạy được trên HTTP/1.1, bỏ bớt streaming. Và vì server nói gRPC còn browser nói
gRPC-Web, phải có ai đó đứng giữa dịch — chính là cổng `:8082` trong demo này.

Đây chính là chỗ "thiếu tự nhiên": **để browser gọi được, bạn phải dựng thêm hạ tầng.** HTTP thì
không cần gì cả — browser sinh ra là để nói HTTP.

### 3. Mất khả năng debug bằng mắt

Với REST, quy trình debug quen thuộc là: mở DevTools → tab Network → click request → đọc response.
Copy cái `curl` gửi cho đồng nghiệp. Paste JSON vào Slack.

Với gRPC:
- DevTools hiện `application/grpc-web+proto` và một đống byte. Không có Preview, không có JSON tree.
- Không `curl` được nếu không tự tay dựng frame (5 byte header + protobuf đã encode).
- Muốn đọc phải có `grpcurl` **và** server phải bật reflection, hoặc phải có sẵn file `.proto`.
- Log ở tầng giữa (nginx, load balancer, APM) chỉ thấy `POST /product.v1.ProductService/ListProducts`
  và một cục nhị phân — không biết gì hơn.

Bytes trong Wire log của demo là bytes thật; bạn có thể tự soi. `0a 29 0a 04 70 30 30 31` — `70 30 30 31`
là `"p001"` ASCII. Đọc được, nhưng phải *giải mã bằng tay*, không phải *nhìn*.

### 4. Vứt bỏ toàn bộ từ vựng chung của web

REST mượn nguyên bộ ngữ nghĩa mà cả internet đã đồng thuận:

| Khái niệm | REST | gRPC |
|---|---|---|
| Định danh tài nguyên | `/api/products/p001` | không có — chỉ có tên method |
| Ý định | `GET` / `POST` / `PUT` / `DELETE` | tất cả đều là `POST` |
| Không tìm thấy | `404` | `codes.NotFound` (HTTP luôn là `200`) |
| Sai input | `400` | `codes.InvalidArgument` |
| Cache | `Cache-Control`, `ETag`, CDN | **không có** |
| Idempotent / safe | `GET`, `PUT` theo chuẩn | tự quy ước với nhau |

Hệ quả thực tế: mọi thứ nằm giữa client và server — CDN, reverse proxy, WAF, API gateway, rate
limiter, browser cache, monitoring — đều **hiểu** HTTP và **mù** với gRPC. Một `GET /api/products`
có thể được CDN cache mà server không cần biết. `ListProducts` thì không, vì với hạ tầng ở giữa nó
chỉ là một `POST` vào một URL lạ với body nhị phân.

Xem `server/grpcsvc/service.go` — có hẳn hàm `translateError` chỉ để dịch `store.ErrNotFound` sang
`codes.NotFound`, trong khi `server/rest/rest.go` chỉ cần `http.StatusNotFound`. Bạn đang phải xây
lại một hệ thống mã lỗi song song với cái web đã có sẵn 30 năm.

### 5. Contract chặt nghĩa là thay đổi phải phối hợp

Trong `product.proto`, mỗi field có một số hiệu:

```protobuf
message Product {
  string id = 1;
  string name = 2;
  int64 price = 3;
  int32 qty = 4;
}
```

Những số này là **vĩnh viễn**. Đổi `price` từ 3 sang 5 thì client cũ đọc dữ liệu mới ra rác — không
lỗi, không cảnh báo, chỉ là sai. Xoá field phải `reserved` số đó lại mãi mãi. Đây là kỷ luật tốt cho
hệ thống lớn, nhưng nó biến "thêm một field" từ việc 30 giây thành một thao tác cần suy nghĩ.

Với JSON, client cũ gặp field lạ thì bỏ qua, thiếu field thì `undefined`. Lỏng lẻo, nhưng tiến hoá dễ.

### 6. Cả những chi tiết nhỏ cũng gợn

Chú ý trong `web/src/api/grpc.ts`:

```ts
await client.createProduct({
  name: draft.name,
  price: BigInt(draft.price),   // int64 → BigInt, JSON.stringify() sẽ ném lỗi với giá trị này
  qty: draft.qty,
})
```

`int64` của protobuf không vừa `number` của JavaScript, nên stub sinh ra dùng `BigInt`. Thế là bạn
phải convert ở biên mỗi lần vào/ra. Còn REST thì `price` chỉ là một số bình thường.

---

## Vậy đổi lại được gì

Không phải gRPC tệ. Nó **không được thiết kế cho browser** — nó được thiết kế cho service gọi
service trong nội bộ, nơi những cái "thiếu tự nhiên" ở trên hoặc biến mất, hoặc trở thành ưu điểm:

| Điểm yếu trên web | Trong nội bộ service-to-service |
|---|---|
| Browser không gọi được | Không liên quan — client là Go/Java/Python |
| Không debug bằng mắt được | Có `grpcurl`, có trace; và không ai debug bằng cách nhìn body |
| Không cache được qua CDN | Nội bộ vốn không đi qua CDN |
| Phải codegen | Chính là cái bạn muốn: 12 service không lệch contract |
| Số hiệu field cứng nhắc | Chính là cái bạn muốn: rolling deploy không vỡ |
| Payload nhị phân | Nhỏ hơn 30–60%, nhân với hàng triệu request nội bộ |

Cộng thêm những thứ REST không có: streaming hai chiều thật, deadline truyền theo call, multiplexing
nhiều RPC trên một kết nối TCP, connection reuse rẻ.

**Kết luận thực dụng:**

- Browser ↔ backend → **HTTP/JSON**. Nếu cần type-safe thì OpenAPI, tRPC, hoặc GraphQL.
- Service ↔ service nội bộ → **gRPC**, rất đáng.
- Bắt buộc phải gọi gRPC từ browser → **Connect-RPC** đáng cân nhắc hơn gRPC-Web: cùng file `.proto`,
  nhưng nói được cả ba giao thức (Connect, gRPC, gRPC-Web) trên cùng một endpoint, và Connect thì
  `curl` được vì nó là POST + JSON.

gRPC không hề "thiếu tự nhiên". Nó chỉ đang đứng nhầm chỗ khi bị đem lên web.
