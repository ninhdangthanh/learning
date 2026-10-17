# Chiều gọi: SNS → Lambda vs SQS → Lambda

Note cho kiến trúc trong `plan.md`.

```
Producer ──push──> SNS ──push(SendMessage)──> SQS <──poll── ESM ──invoke──> Lambda A/B
                    │
                    └──push(InvokeFunction)──────────────────────────────> Lambda C
```

---

## 1. SNS → Lambda: SNS GỌI (push) Lambda

SNS chủ động gọi `lambda:InvokeFunction`. Chữ "subscribe" chỉ có nghĩa là *đăng ký endpoint* vào topic,
không phải Lambda đi kéo dữ liệu về.

Bằng chứng trong plan (Bước 4.2):

```bash
aws lambda add-permission \
    --function-name sns-handler \
    --principal sns.amazonaws.com \      # ← SNS là bên GỌI
    --action lambda:InvokeFunction \
    --source-arn $TOPIC_ARN
```

Phải cấp **resource-based policy** trên Lambda cho `sns.amazonaws.com` vì SNS là caller.
Nếu Lambda tự đi lấy thì đã không cần dòng này. Thiếu dòng này → SNS không gọi được, lỗi im lặng.

**Đặc điểm:**
- **Async invoke** — SNS bắn xong là quên.
- Lambda lỗi → AWS retry 2 lần nữa (tổng 3 lần), hết thì **message mất**,
  trừ khi cấu hình On-failure destination / DLQ trên Lambda.
- **Không batch**: 1 message SNS = 1 invocation, `events.SNSEvent` chứa đúng 1 record.
- Không có backpressure: burst bao nhiêu thì invoke bấy nhiêu.

---

## 2. SQS → Lambda: Lambda POLL (consume) từ SQS

SQS hoàn toàn thụ động — **nó không bao giờ gọi ai cả**. Không có "SQS trigger Lambda" theo nghĩa đen.

Thứ thực sự chạy là **Event Source Mapping (ESM)** — một poller do Lambda service quản lý,
nằm *ngoài* function code của mình:

```bash
aws lambda create-event-source-mapping \
    --function-name payment-handler \
    --event-source-arn $PAYMENT_QUEUE_ARN \
    --batch-size 5 \
    --maximum-batching-window-in-seconds 10
```

**Vòng đời một batch:**

1. ESM long-poll `sqs:ReceiveMessage`
2. Gom tối đa 5 message (`--batch-size`) hoặc chờ hết 10s (`--maximum-batching-window-in-seconds`)
3. **Synchronous invoke** handler với `events.SQSEvent`
4. Handler `return nil` → ESM gọi `sqs:DeleteMessage`
5. Handler trả về error → **không xóa**, cả batch quay lại queue sau visibility timeout

Bằng chứng trong plan (Bước 0.3): attach `AWSLambdaSQSQueueExecutionRole` vào
**execution role của Lambda** — role này chứa `sqs:ReceiveMessage`, `sqs:DeleteMessage`,
`sqs:GetQueueAttributes`.

Chiều quyền **ngược hẳn** với case SNS: ở đây Lambda cần quyền *đọc* SQS,
chứ không phải SQS cần quyền *gọi* Lambda. Queue policy của PaymentQueue trong plan
chỉ mở cho `sns.amazonaws.com`, không có dòng nào cho Lambda — và như vậy là đủ.

---

## 3. SNS → SQS: push

SNS gọi `sqs:SendMessage` vào queue → đó là lý do Bước 2.1 phải set **queue policy**
cho `Principal: sns.amazonaws.com`. Thiếu là message rơi im lặng, không có lỗi gì báo về.

---

## Bảng so sánh

| | SNS → Lambda C | SQS → Lambda A/B |
|---|---|---|
| Ai khởi xướng | SNS | Lambda ESM (poller) |
| Kiểu invoke | Async | Sync (ESM giữ kết quả) |
| Cần quyền gì | Resource policy trên Lambda cho SNS | Execution role của Lambda có quyền SQS |
| Batch | Không, 1-1 | Có, `--batch-size 5` |
| Retry khi lỗi | 2 lần rồi mất (nếu không DLQ) | Tới `maxReceiveCount`, rồi vào DLQ |
| Backpressure | Không | Có — poller điều tiết theo concurrency |

**Vì sao chèn SQS vào giữa:** Lambda C nhận nguyên cú burst từ SNS và mất message nếu fail 3 lần;
Lambda A/B có buffer, có batch, có DLQ thật sự.

---

## Ghi chú thêm về plan hiện tại

- Subscription SQS set `RawMessageDelivery=true` → `message.Body` trong payment/email handler
  là JSON gốc, parse thẳng được, không bị bọc SNS envelope.
- Lambda C dùng protocol `lambda`, không set attribute đó → vẫn đọc qua `snsRecord.Message`,
  đúng như code `sns-handler/main.go` đang viết.
- Khi handler SQS lỗi, **cả batch** quay lại chứ không riêng message lỗi —
  muốn chỉ retry message lỗi thì bật `ReportBatchItemFailures` trên ESM
  và trả về `events.SQSEventResponse` với danh sách `BatchItemFailures`.
