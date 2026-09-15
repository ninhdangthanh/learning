## 🎯 Kiến trúc demo 

```
                    ┌──> SQS PaymentQueue ──> Lambda A (xử lý thanh toán)
                    │
[Producer] ──> SNS ─┼──> SQS EmailQueue ────> Lambda B (gửi email)
                    │
                    └──> Lambda C (trực tiếp, không qua SQS)
```

---

## 🛠️ Bước 0: Chuẩn bị (mới, chỉ có ở AWS thật)

### 0.1. Cấu hình AWS CLI

```bash
aws configure
# Nhập Access Key ID, Secret Access Key, region (ví dụ: ap-southeast-1), output format (json)
```

### 0.2. Chọn region và lấy Account ID

```bash
export AWS_REGION=ap-southeast-1
export ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
echo "Account ID: $ACCOUNT_ID"
```

> **Lưu ý:** Tất cả service (SNS, SQS, Lambda) **phải cùng region**. Nếu khác region, SNS không subscribe được SQS cross-region (trừ khi dùng SNS FIFO, càng phức tạp).

### 0.3. Tạo IAM Role cho Lambda

Đây là bước **không có trong LocalStack** nhưng **bắt buộc** trên AWS thật.

**Tạo file `trust-policy.json`:**
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Service": "lambda.amazonaws.com" },
      "Action": "sts:AssumeRole"
    }
  ]
}
```

**Tạo role:**
```bash
aws iam create-role \
    --role-name LambdaDemoRole \
    --assume-role-policy-document file://trust-policy.json
```

**Gắn policy để Lambda ghi log và đọc SQS:**
```bash
# Policy cơ bản cho Lambda ghi CloudWatch Logs
aws iam attach-role-policy \
    --role-name LambdaDemoRole \
    --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole

# Policy cho Lambda đọc SQS
aws iam attach-role-policy \
    --role-name LambdaDemoRole \
    --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaSQSQueueExecutionRole
```

**Lấy ARN của role:**
```bash
export ROLE_ARN=$(aws iam get-role --role-name LambdaDemoRole --query Role.Arn --output text)
echo "Role ARN: $ROLE_ARN"
```

> ⏳ **Quan trọng:** Sau khi tạo role, đợi khoảng **10-15 giây** để IAM propagate trước khi tạo Lambda, nếu không sẽ bị lỗi `The role defined for the function cannot be assumed by Lambda`.

---

## 📦 Bước 1: Tạo SNS Topic và SQS Queues

### 1.1. Tạo SNS Topic

```bash
export TOPIC_ARN=$(aws sns create-topic \
    --name OrderEventsTopic \
    --query TopicArn --output text)
echo "Topic ARN: $TOPIC_ARN"
```

### 1.2. Tạo 2 SQS Queue

```bash
export PAYMENT_QUEUE_URL=$(aws sqs create-queue \
    --queue-name PaymentQueue \
    --query QueueUrl --output text)

export EMAIL_QUEUE_URL=$(aws sqs create-queue \
    --queue-name EmailQueue \
    --query QueueUrl --output text)

echo "Payment Queue: $PAYMENT_QUEUE_URL"
echo "Email Queue:   $EMAIL_QUEUE_URL"
```

**Lấy ARN của queue** (cần cho bước subscribe):

```bash
export PAYMENT_QUEUE_ARN=$(aws sqs get-queue-attributes \
    --queue-url $PAYMENT_QUEUE_URL \
    --attribute-names QueueArn \
    --query Attributes.QueueArn --output text)

export EMAIL_QUEUE_ARN=$(aws sqs get-queue-attributes \
    --queue-url $EMAIL_QUEUE_URL \
    --attribute-names QueueArn \
    --query Attributes.QueueArn --output text)

echo "Payment Queue ARN: $PAYMENT_QUEUE_ARN"
echo "Email Queue ARN:   $EMAIL_QUEUE_ARN"
```

---

## 🔗 Bước 2: Subscribe SQS vào SNS

Trên AWS thật, **SQS phải cho phép SNS gửi message vào** — đây là bước hay bị quên nhất. Bạn cần set **Queue Policy**.

### 2.1. Set Queue Policy cho PaymentQueue

**Tạo file `payment-queue-policy.json`:**
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Service": "sns.amazonaws.com" },
      "Action": "sqs:SendMessage",
      "Resource": "PAYMENT_QUEUE_ARN_PLACEHOLDER",
      "Condition": {
        "ArnEquals": { "aws:SourceArn": "TOPIC_ARN_PLACEHOLDER" }
      }
    }
  ]
}
```

**Thay placeholder bằng giá trị thật và apply:**
```bash
sed -e "s|PAYMENT_QUEUE_ARN_PLACEHOLDER|$PAYMENT_QUEUE_ARN|" \
    -e "s|TOPIC_ARN_PLACEHOLDER|$TOPIC_ARN|" \
    payment-queue-policy.json > payment-queue-policy-final.json

aws sqs set-queue-attributes \
    --queue-url $PAYMENT_QUEUE_URL \
    --attributes Policy="$(cat payment-queue-policy-final.json)"
```

### 2.2. Làm tương tự cho EmailQueue

```bash
sed -e "s|PAYMENT_QUEUE_ARN_PLACEHOLDER|$EMAIL_QUEUE_ARN|" \
    -e "s|TOPIC_ARN_PLACEHOLDER|$TOPIC_ARN|" \
    payment-queue-policy.json > email-queue-policy-final.json

aws sqs set-queue-attributes \
    --queue-url $EMAIL_QUEUE_URL \
    --attributes Policy="$(cat email-queue-policy-final.json)"
```

### 2.3. Subscribe SQS vào SNS

```bash
# Subscribe PaymentQueue
aws sns subscribe \
    --topic-arn $TOPIC_ARN \
    --protocol sqs \
    --notification-endpoint $PAYMENT_QUEUE_ARN \
    --attributes '{"RawMessageDelivery":"true"}'

# Subscribe EmailQueue
aws sns subscribe \
    --topic-arn $TOPIC_ARN \
    --protocol sqs \
    --notification-endpoint $EMAIL_QUEUE_ARN \
    --attributes '{"RawMessageDelivery":"true"}'
```

> **RawMessageDelivery=true** giúp Lambda nhận được JSON gốc, không bị bọc trong SNS envelope.

---

## ⚡ Bước 3: Tạo 3 Lambda Functions (Golang)

### 3.1. Cấu trúc project

```
demo-sns-sqs/
├── payment-handler/
│   ├── main.go
│   └── bootstrap        # file build
├── email-handler/
│   ├── main.go
│   └── bootstrap
└── sns-handler/
    ├── main.go
    └── bootstrap
```

> **Quan trọng:** Lambda Go runtime dùng **`provided.al2023`** (custom runtime) và file build phải tên là **`bootstrap`**, không phải tên gì khác.

### 3.2. Code 3 handler

**`payment-handler/main.go`:**
```go
package main

import (
    "context"
    "fmt"
    "github.com/aws/aws-lambda-go/events"
    "github.com/aws/aws-lambda-go/lambda"
)

func handler(ctx context.Context, sqsEvent events.SQSEvent) error {
    for _, message := range sqsEvent.Records {
        fmt.Printf("[PAYMENT] Xử lý đơn hàng: %s\n", message.Body)
    }
    return nil
}

func main() {
    lambda.Start(handler)
}
```

**`email-handler/main.go`:**
```go
package main

import (
    "context"
    "fmt"
    "github.com/aws/aws-lambda-go/events"
    "github.com/aws/aws-lambda-go/lambda"
)

func handler(ctx context.Context, sqsEvent events.SQSEvent) error {
    for _, message := range sqsEvent.Records {
        fmt.Printf("[EMAIL] Gửi email cho đơn hàng: %s\n", message.Body)
    }
    return nil
}

func main() {
    lambda.Start(handler)
}
```

**`sns-handler/main.go`:**
```go
package main

import (
    "context"
    "fmt"
    "github.com/aws/aws-lambda-go/events"
    "github.com/aws/aws-lambda-go/lambda"
)

func handler(ctx context.Context, snsEvent events.SNSEvent) error {
    for _, record := range snsEvent.Records {
        snsRecord := record.SNS
        fmt.Printf("[SNS-DIRECT] MessageId: %s\n", snsRecord.MessageID)
        fmt.Printf("[SNS-DIRECT] TopicArn:  %s\n", snsRecord.TopicArn)
        fmt.Printf("[SNS-DIRECT] Message:   %s\n", snsRecord.Message)
    }
    return nil
}

func main() {
    lambda.Start(handler)
}
```

### 3.3. Build và đóng gói

```bash
cd demo-sns-sqs

# Build cho Linux (Lambda chạy Linux)
for dir in payment-handler email-handler sns-handler; do
    cd $dir
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bootstrap main.go
    zip -j function.zip bootstrap
    cd ..
done
```

### 3.4. Deploy 3 Lambda lên AWS

```bash
# Lambda A - Payment
export PAYMENT_LAMBDA_ARN=$(aws lambda create-function \
    --function-name payment-handler \
    --runtime provided.al2023 \
    --role $ROLE_ARN \
    --handler bootstrap \
    --zip-file fileb://payment-handler/function.zip \
    --timeout 30 \
    --memory-size 128 \
    --query FunctionArn --output text)

# Lambda B - Email
export EMAIL_LAMBDA_ARN=$(aws lambda create-function \
    --function-name email-handler \
    --runtime provided.al2023 \
    --role $ROLE_ARN \
    --handler bootstrap \
    --zip-file fileb://email-handler/function.zip \
    --timeout 30 \
    --memory-size 128 \
    --query FunctionArn --output text)

# Lambda C - SNS Direct
export SNS_LAMBDA_ARN=$(aws lambda create-function \
    --function-name sns-handler \
    --runtime provided.al2023 \
    --role $ROLE_ARN \
    --handler bootstrap \
    --zip-file fileb://sns-handler/function.zip \
    --timeout 30 \
    --memory-size 128 \
    --query FunctionArn --output text)

echo "Payment Lambda: $PAYMENT_LAMBDA_ARN"
echo "Email Lambda:   $EMAIL_LAMBDA_ARN"
echo "SNS Lambda:     $SNS_LAMBDA_ARN"
```

---

## 🔌 Bước 4: Kết nối Lambda với SQS và SNS

### 4.1. Gắn SQS trigger cho Lambda A và B

```bash
# Payment Lambda đọc PaymentQueue
aws lambda create-event-source-mapping \
    --function-name payment-handler \
    --event-source-arn $PAYMENT_QUEUE_ARN \
    --batch-size 5 \
    --maximum-batching-window-in-seconds 10

# Email Lambda đọc EmailQueue
aws lambda create-event-source-mapping \
    --function-name email-handler \
    --event-source-arn $EMAIL_QUEUE_ARN \
    --batch-size 5 \
    --maximum-batching-window-in-seconds 10
```

### 4.2. Cấp permission cho SNS gọi Lambda C

**Đây là bước quan trọng nhất khi làm bằng CLI** — thiếu là SNS không gọi được Lambda, nhưng lỗi thường im lặng:

```bash
aws lambda add-permission \
    --function-name sns-handler \
    --statement-id sns-invoke \
    --action lambda:InvokeFunction \
    --principal sns.amazonaws.com \
    --source-arn $TOPIC_ARN
```

### 4.3. Subscribe Lambda C trực tiếp vào SNS

```bash
aws sns subscribe \
    --topic-arn $TOPIC_ARN \
    --protocol lambda \
    --notification-endpoint $SNS_LAMBDA_ARN
```

---

## 🧪 Bước 5: Kiểm thử

### 5.1. Publish message vào SNS

```bash
aws sns publish \
    --topic-arn $TOPIC_ARN \
    --subject "New Order" \
    --message '{"orderId":"ORD-001","customer":"Nam","amount":500000}'
```

### 5.2. Kiểm tra message trong 2 queue

```bash
# Xem message trong PaymentQueue (không xóa)
aws sqs receive-message --queue-url $PAYMENT_QUEUE_URL --max-number-of-messages 10

# Xem message trong EmailQueue
aws sqs receive-message --queue-url $EMAIL_QUEUE_URL --max-number-of-messages 10
```

> **Lưu ý:** Nếu Lambda trigger đã hoạt động, message sẽ bị Lambda "kéo" đi rất nhanh, có thể bạn không kịp thấy. Để quan sát, bạn có thể **tạm disable trigger** bằng cách xóa event source mapping, rồi publish lại.

### 5.3. Xem log của 3 Lambda

```bash
# Xem log Payment Lambda
aws logs tail /aws/lambda/payment-handler --follow

# Xem log Email Lambda
aws logs tail /aws/lambda/email-handler --follow

# Xem log SNS Direct Lambda
aws logs tail /aws/lambda/sns-handler --follow
```

Hoặc mở **CloudWatch Logs** trên Console → Log groups → chọn `/aws/lambda/<function-name>`.

---

## 💰 Bước 6: Dọn dẹp (quan trọng với AWS thật)

Để tránh phát sinh chi phí, xóa theo thứ tự:

```bash
# 1. Xóa event source mappings
aws lambda list-event-source-mappings --function-name payment-handler --query "EventSourceMappings[].UUID" --output text | xargs -I {} aws lambda delete-event-source-mapping --uuid {}
aws lambda list-event-source-mappings --function-name email-handler --query "EventSourceMappings[].UUID" --output text | xargs -I {} aws lambda delete-event-source-mapping --uuid {}

# 2. Xóa Lambda functions
aws lambda delete-function --function-name payment-handler
aws lambda delete-function --function-name email-handler
aws lambda delete-function --function-name sns-handler

# 3. Xóa SNS subscriptions
aws sns list-subscriptions-by-topic --topic-arn $TOPIC_ARN --query "Subscriptions[].SubscriptionArn" --output text | xargs -I {} aws sns unsubscribe --subscription-arn {}

# 4. Xóa SNS topic
aws sns delete-topic --topic-arn $TOPIC_ARN

# 5. Xóa SQS queues
aws sqs delete-queue --queue-url $PAYMENT_QUEUE_URL
aws sqs delete-queue --queue-url $EMAIL_QUEUE_URL

# 6. Xóa IAM role (detach policy trước)
aws iam detach-role-policy --role-name LambdaDemoRole --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole
aws iam detach-role-policy --role-name LambdaDemoRole --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaSQSQueueExecutionRole
aws iam delete-role --role-name LambdaDemoRole
```