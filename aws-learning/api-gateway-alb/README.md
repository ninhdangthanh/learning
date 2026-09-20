# fnb — API Gateway + ALB + ECS Fargate

Lab theo `guide.md`. Kiến trúc: API Gateway → ALB → ECS Fargate (order-service, product-service) + Lambda.

---

## ⚠️ Mỗi tab terminal mới: nạp env TRƯỚC khi chạy bất cứ lệnh nào

```bash
cd /Users/dangthanhninh/Documents/NinhData/aws-learn/aws-learning/api-gateway-alb
source ./env.sh && echo "PROJECT=$PROJECT  VPC_ID=$VPC_ID  CLUSTER=$CLUSTER"
```

Ba giá trị phải hiện ra đầy đủ. Rỗng bất kỳ cái nào thì **dừng lại**, đừng chạy tiếp.

### Vì sao

Toàn bộ guide chạy bằng biến môi trường (`$PROJECT`, `$VPC_ID`, `$ALB_ARN`, `$PRODUCT_TG`...). Biến môi trường **chỉ sống trong đúng shell đã tạo ra nó**:

- Mở tab terminal mới → shell mới → không có biến nào
- `save FOO bar` chỉ export vào shell đang chạy nó; tab khác không thấy
- Nhờ agent/script chạy hộ → mỗi lệnh là một shell riêng, export bay mất ngay sau đó

### Nguy hiểm ở chỗ nào

Shell **không báo lỗi khi biến rỗng**. Nó lặng lẽ thay bằng chuỗi rỗng rồi để AWS CLI nhận một lệnh méo mó. Lỗi hiện ra ở tận tầng AWS, không liên quan gì tới nguyên nhân thật:

| Lỗi nhìn thấy | Nguyên nhân thật |
|---|---|
| `zsh: command not found: save` | chưa `source` — đây là dấu hiệu sớm nhất |
| `argument --vpc-id: expected one argument` | `$VPC_ID` rỗng |
| `argument --pool-name: expected one argument` | `$PROJECT` rỗng → `-pool` bị hiểu là tên option |
| `Container.image should not be null or empty` | `$PRODUCT_IMAGE` rỗng lúc generate taskdef |
| `Invalid ARN` / `ValidationException` | một biến ARN nào đó rỗng |

Heredoc (`cat > file.json <<EOF`) là chỗ nguy hiểm nhất: biến rỗng được ghi thẳng vào file, không một cảnh báo nào, và bạn chỉ phát hiện khi AWS từ chối file đó.

### Nguồn chân lý duy nhất

`./env.sh` trong chính thư mục này. File tự chứa đủ mọi thứ — base config, mọi ARN/ID do `save` sinh ra, và các helper (`save`, `get_task_ips`).

`save TEN_BIEN gia_tri` sẽ append thẳng vào file này, nên mọi giá trị mới đều nằm trong project và đi theo git.

Đường dẫn ghi được quyết định bởi `$FNB_ENV_FILE` (đặt sẵn ở dòng đầu `env.sh`). Nếu di chuyển thư mục project, sửa lại đúng một dòng đó.

### Thói quen nên có

Trước mỗi block lệnh dùng biến, `echo` ra để xác nhận:

```bash
echo "$EXEC_ROLE_ARN | $PRODUCT_IMAGE"
```

Tốn 1 giây, tiết kiệm 20 phút debug nhầm hướng.

---

## Script xem trạng thái hệ thống

Chạy nối đuôi là ra đúng đường đi của một request, từ ngoài vào trong:

```bash
./apigw.sh   # routes → integration URI
./alb.sh     # listener → rules → target group
./tg.sh      # target group: IP nào đang đăng ký, healthy không
./ecs.sh     # service, task, deployment, events
```

Không cần `source env.sh`, các script tự resolve theo tên. Đều nhận tham số để lọc:

```bash
./tg.sh fnb-product-tg
./ecs.sh fnb-cluster
./apigw.sh lzwtbflg8i
```

Xem cuốn sổ target group thay đổi theo thời gian thực:

```bash
while true; do clear; ./tg.sh fnb-product-tg; sleep 3; done
```

---

## Bốn kiểu 404, phân biệt bằng body

Cùng status `404` nhưng do bốn tầng khác nhau trả về — đọc body là biết request chết ở đâu:

| Body | Ai trả lời |
|---|---|
| `{"message":"Not Found"}` | API Gateway — route key không khớp, chưa tới ALB |
| `{"error":"no ALB rule matched"}` | ALB default action — tới ALB, không rule nào khớp |
| `404 page not found` | Go ServeMux — tới app, app không có route đó |
| `{"error":"product not found","id":"..."}` | handler — route đúng, dữ liệu không tồn tại |
