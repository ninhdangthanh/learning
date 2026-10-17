# Hướng dẫn chi tiết — API Gateway + ALB + ECS Fargate + Lambda

Đây là bản **thực thi từng bước** của `plan.md`. Mỗi bước có đủ code + lệnh CLI + cách verify.

## 🎯 Kiến trúc cuối cùng

```
                          INTERNET
                             │
                             ▼
                   ┌──────────────────┐
                   │   API GATEWAY    │
                   │  HTTP API        │
                   │  • Route         │
                   │  • JWT Auth      │
                   │  • Throttling    │
                   └────────┬─────────┘
                            │
            ┌───────────────┴───────────────┐
            │ HTTP_PROXY                    │ AWS_PROXY
            ▼                               ▼
     ┌─────────────┐                  ┌──────────┐
     │     ALB     │                  │  Lambda  │
     │ • Listener  │                  │ /system/ │
     │ • Rules     │                  │   info   │
     │ • Health    │                  └──────────┘
     └──────┬──────┘
            │
    ┌───────┴────────┐
    ▼                ▼
/orders*        /products*
    │                │
    ▼                ▼
┌────────┐       ┌────────┐
│ Order  │       │Product │
│  TG    │       │  TG    │
└───┬────┘       └───┬────┘
    │                │
 ECS #1 #2 #3     ECS #1 #2
   (Fargate)       (Fargate)
```

## 🗺️ Map phase trong plan.md → bước trong file này

| Phase (plan.md) | Bước (file này) |
|---|---|
| Phase 1 — Go API local | Bước 1 |
| Phase 2 — Dockerize | Bước 2 |
| Phase 3 — ECS Fargate | Bước 3, 4, 5 |
| Phase 4 — Nhiều instance | Bước 6 |
| Phase 5 — ALB | Bước 7 |
| Phase 6 — ALB Routing | Bước 8 |
| Phase 7, 8 — API Gateway | Bước 9 |
| Phase 9 — API Gateway → Lambda | Bước 10 |
| Phase 10 — JWT | Bước 11 |
| Phase 11 — Throttling | Bước 12 |
| Phase 12 — Monitoring | Bước 13 |
| Phase 13 — Final | Bước 14 (khoá ALB), Bước 15 (cleanup) |

> **Khác biệt so với plan.md:** plan.md có chỗ vẽ MongoDB trong flow, nhưng phần *Tech stack* ghi rõ
> *"Không cần dùng database, chỉ fmt.print"*. File này theo tech stack: **dữ liệu in-memory, không MongoDB**.
> Bỏ DB đi giúp tập trung 100% vào lớp network (API Gateway / ALB / ECS) — đúng mục tiêu project.

---

## 🛠️ Bước 0: Chuẩn bị

### 0.1. Công cụ cần có

```bash
aws --version       # >= 2.x
docker --version
go version          # >= 1.22 (dùng pattern "GET /orders/{id}" của net/http)
jq --version
k6 version          # cài sau cũng được, chỉ dùng ở Bước 12
```

### 0.2. Cấu hình AWS CLI

```bash
aws configure
# Access Key ID, Secret Access Key, region (ap-southeast-1), output format (json)
```

### 0.3. File biến môi trường

Project này có **rất nhiều ARN/ID**. Đóng terminal là mất sạch. Nên ghi ra file và `source` lại mỗi lần mở terminal mới.

> ## ⚠️ Việc đầu tiên của MỌI tab terminal mới
>
> ```bash
> cd <thu-muc-project> && source ./env.sh
> echo "PROJECT=$PROJECT  VPC_ID=$VPC_ID  CLUSTER=$CLUSTER"
> ```
>
> Ba giá trị phải hiện ra đầy đủ. Rỗng bất kỳ cái nào thì **dừng lại**, đừng chạy tiếp.
>
> Biến môi trường chỉ sống trong đúng shell đã tạo ra nó. Tab mới = shell mới = không có biến nào. Và `save FOO bar` cũng chỉ export vào shell đang chạy nó, tab khác không thấy.
>
> **Bỏ qua bước này thì shell không báo lỗi.** Nó lặng lẽ thay biến rỗng vào lệnh, rồi AWS CLI trả về một lỗi chẳng liên quan gì tới nguyên nhân thật:
>
> | Lỗi nhìn thấy | Nguyên nhân thật |
> |---|---|
> | `command not found: save` | chưa `source` — dấu hiệu sớm nhất |
> | `argument --vpc-id: expected one argument` | `$VPC_ID` rỗng |
> | `argument --pool-name: expected one argument` | `$PROJECT` rỗng → `-pool` bị hiểu là tên option |
> | `Container.image should not be null or empty` | `$PRODUCT_IMAGE` rỗng lúc generate taskdef |
> | `Invalid ARN` / `ValidationException` | một biến ARN nào đó rỗng |
>
> Nguy hiểm nhất là heredoc (`cat > file.json <<EOF`): biến rỗng ghi thẳng vào file, không một cảnh báo nào, và bạn chỉ phát hiện khi AWS từ chối file đó.

Env đặt trong **chính thư mục project** (không phải `~/aws-fnb`) để mọi thứ đi theo git cùng code:

```bash
cd <thu-muc-project>

cat > env.sh <<EOF
FNB_ENV_FILE="\${FNB_ENV_FILE:-$PWD/env.sh}"
EOF

cat >> env.sh <<'EOF'

save() { echo "export $1=$2" >> "$FNB_ENV_FILE"; export "$1=$2"; echo "$1 = $2"; }

export AWS_REGION=ap-southeast-1
export AWS_DEFAULT_REGION=$AWS_REGION
export PROJECT=fnb
EOF

echo "export ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)" >> env.sh

source ./env.sh
echo "Account: $ACCOUNT_ID / Region: $AWS_REGION"
```

Heredoc đầu **không** nháy `EOF` để `$PWD` được bung ra thành đường dẫn thật. Heredoc sau **có** nháy để `$1`/`$2` trong hàm `save` giữ nguyên.

Quy ước từ đây: mỗi khi lấy được một giá trị mới, dùng `save TEN_BIEN gia_tri` thay cho `export` — biến vừa export vào shell hiện tại, vừa được append vào `env.sh` để tab sau còn dùng.

> Di chuyển thư mục project thì sửa lại đúng dòng `FNB_ENV_FILE` ở đầu `env.sh`.

### 0.4. Lấy VPC và subnet mặc định

ECS Fargate + ALB đều cần subnet. Dùng **default VPC** cho nhanh.

```bash
save VPC_ID "$(aws ec2 describe-vpcs --filters Name=isDefault,Values=true \
    --query 'Vpcs[0].VpcId' --output text)"

save SUBNETS "$(aws ec2 describe-subnets --filters Name=vpc-id,Values=$VPC_ID \
    --query 'Subnets[].SubnetId' --output text | tr '\t' ',')"

echo "VPC: $VPC_ID"
echo "Subnets: $SUBNETS"
```

> ⚠️ **ALB cần tối thiểu 2 subnet ở 2 Availability Zone khác nhau.** Nếu `$SUBNETS` chỉ ra 1 subnet,
> ALB sẽ fail với `At least two subnets in two different Availability Zones must be specified`.
> Kiểm tra: `aws ec2 describe-subnets --filters Name=vpc-id,Values=$VPC_ID --query 'Subnets[].AvailabilityZone'`

---

## 📝 Bước 1 (Phase 1) — Viết 2 Go service

### 1.1. Cấu trúc project

```
~/aws-fnb/
├── env.sh
├── order-service/
│   ├── go.mod
│   ├── main.go
│   └── Dockerfile
├── product-service/
│   ├── go.mod
│   ├── main.go
│   └── Dockerfile
├── system-info-lambda/
│   ├── go.mod
│   └── main.go
└── docker-compose.yml
```

### 1.2. `order-service/main.go`

```go
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

type Order struct {
	ID       string `json:"id"`
	Product  string `json:"product"`
	Quantity int    `json:"quantity"`
	Status   string `json:"status"`
}

var (
	mu     sync.RWMutex
	seq    = 2
	orders = map[string]Order{
		"1": {ID: "1", Product: "Ca phe sua", Quantity: 2, Status: "NEW"},
		"2": {ID: "2", Product: "Banh mi", Quantity: 1, Status: "DONE"},
	}
	healthy    atomic.Bool
	instanceID = resolveInstanceID()
)

func resolveInstanceID() string {
	if v := os.Getenv("INSTANCE_ID"); v != "" {
		return v
	}
	host, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Instance", instanceID)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func listOrders(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	defer mu.RUnlock()
	items := make([]Order, 0, len(orders))
	for _, o := range orders {
		items = append(items, o)
	}
	log.Printf("[order] GET /orders -> %d items (instance=%s)", len(items), instanceID)
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "orders": items})
}

func getOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mu.RLock()
	o, ok := orders[id]
	mu.RUnlock()
	log.Printf("[order] GET /orders/%s found=%v (instance=%s)", id, ok, instanceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found", "id": id})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "order": o})
}

func createOrder(w http.ResponseWriter, r *http.Request) {
	var in Order
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	mu.Lock()
	seq++
	in.ID = strconv.Itoa(seq)
	if in.Status == "" {
		in.Status = "NEW"
	}
	orders[in.ID] = in
	mu.Unlock()
	log.Printf("[order] POST /orders id=%s product=%s (instance=%s)", in.ID, in.Product, instanceID)
	writeJSON(w, http.StatusCreated, map[string]any{"instance": instanceID, "order": in})
}

func deleteOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mu.Lock()
	_, ok := orders[id]
	delete(orders, id)
	mu.Unlock()
	log.Printf("[order] DELETE /orders/%s existed=%v (instance=%s)", id, ok, instanceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found", "id": id})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "deleted": id})
}

func health(w http.ResponseWriter, r *http.Request) {
	if !healthy.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"instance": instanceID, "status": "unhealthy"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "status": "ok"})
}

func toggleHealth(w http.ResponseWriter, r *http.Request) {
	healthy.Store(!healthy.Load())
	log.Printf("[order] health toggled -> %v (instance=%s)", healthy.Load(), instanceID)
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "healthy": healthy.Load()})
}

func main() {
	healthy.Store(true)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", listOrders)
	mux.HandleFunc("GET /orders/{id}", getOrder)
	mux.HandleFunc("POST /orders", createOrder)
	mux.HandleFunc("DELETE /orders/{id}", deleteOrder)
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("POST /admin/toggle-health", toggleHealth)

	log.Printf("order-service listening on :%s (instance=%s)", port, instanceID)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
```

**`order-service/go.mod`:**
```
module order-service

go 1.22
```

> **Tại sao mọi response đều có `instance`?** Vì từ Bước 6 trở đi bạn chạy nhiều task ECS.
> Không có field này thì **không cách nào nhìn thấy** load balancing đang hoạt động —
> mọi response trông giống hệt nhau. Đây là cái mỏ neo để quan sát toàn bộ ALB behavior.

> **`POST /admin/toggle-health` để làm gì?** Ở Bước 7.6 bạn cần một target **unhealthy thật**
> để nhìn ALB rút nó khỏi vòng quay. Không có endpoint này thì phải đi kill task,
> vừa chậm vừa bị ECS tự spawn task mới thay thế.

### 1.3. `product-service/main.go`

```go
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
)

type Product struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

var (
	mu       sync.RWMutex
	seq      = 3
	products = map[string]Product{
		"1": {ID: "1", Name: "Ca phe sua", Price: 25000},
		"2": {ID: "2", Name: "Banh mi", Price: 20000},
		"3": {ID: "3", Name: "Tra dao", Price: 35000},
	}
	instanceID = resolveInstanceID()
)

func resolveInstanceID() string {
	if v := os.Getenv("INSTANCE_ID"); v != "" {
		return v
	}
	host, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Instance", instanceID)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func listProducts(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	defer mu.RUnlock()
	items := make([]Product, 0, len(products))
	for _, p := range products {
		items = append(items, p)
	}
	log.Printf("[product] GET /products -> %d items (instance=%s)", len(items), instanceID)
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "products": items})
}

func getProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mu.RLock()
	p, ok := products[id]
	mu.RUnlock()
	log.Printf("[product] GET /products/%s found=%v (instance=%s)", id, ok, instanceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "product not found", "id": id})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "product": p})
}

func createProduct(w http.ResponseWriter, r *http.Request) {
	var in Product
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	mu.Lock()
	seq++
	in.ID = strconv.Itoa(seq)
	products[in.ID] = in
	mu.Unlock()
	log.Printf("[product] POST /products id=%s name=%s (instance=%s)", in.ID, in.Name, instanceID)
	writeJSON(w, http.StatusCreated, map[string]any{"instance": instanceID, "product": in})
}

func health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "status": "ok"})
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /products", listProducts)
	mux.HandleFunc("GET /products/{id}", getProduct)
	mux.HandleFunc("POST /products", createProduct)
	mux.HandleFunc("GET /health", health)

	log.Printf("product-service listening on :%s (instance=%s)", port, instanceID)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
```

**`product-service/go.mod`:**
```
module product-service

go 1.22
```

### 1.4. Chạy local và test

```bash
cd ~/aws-fnb/order-service   && go run main.go &
cd ~/aws-fnb/product-service && go run main.go &

curl -s localhost:8080/orders   | jq
curl -s localhost:8080/orders/1 | jq
curl -s localhost:8080/health   | jq
curl -s localhost:8081/products | jq

curl -s -X POST localhost:8080/orders \
  -H 'Content-Type: application/json' \
  -d '{"product":"Tra dao","quantity":3}' | jq

curl -s -X DELETE localhost:8080/orders/2 | jq
```

### ✅ Checkpoint Phase 1

Bạn phải trả lời được: request đi từ **HTTP request → ServeMux (router) → Handler → in-memory store** như thế nào.
Chưa có AWS gì ở đây cả.

```bash
kill %1 %2
```

---

## 🐳 Bước 2 (Phase 2) — Dockerize

### 2.1. `order-service/Dockerfile`

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server main.go

FROM alpine:3.20
RUN apk add --no-cache curl ca-certificates
COPY --from=builder /out/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
```

`product-service/Dockerfile` giống hệt, chỉ đổi `EXPOSE 8081`.

> **Multi-stage build:** stage 1 có toàn bộ Go toolchain (~800MB), stage 2 chỉ copy binary sang alpine (~15MB).
> Image nhỏ = ECS pull nhanh = task start nhanh. Đây là lý do cold start của ECS phụ thuộc rất nhiều vào image size.

### 2.2. `docker-compose.yml`

```yaml
services:
  order-service:
    build: ./order-service
    ports:
      - "8080:8080"
    environment:
      PORT: "8080"
      INSTANCE_ID: "order-local"

  product-service:
    build: ./product-service
    ports:
      - "8081:8081"
    environment:
      PORT: "8081"
      INSTANCE_ID: "product-local"
```

### 2.3. Chạy và test

```bash
cd ~/aws-fnb
docker compose up --build -d
docker compose ps

curl -s localhost:8080/orders   | jq
curl -s localhost:8081/products | jq

docker compose logs -f order-service
```

### ✅ Checkpoint Phase 2

```
Dockerfile → docker build → Image → docker run → Container → HTTP Server
```

Bạn hiểu: **image là bản đóng gói tĩnh, container là tiến trình đang chạy từ image đó.**
ECS sau này chỉ làm đúng một việc: chạy container từ image, trên máy của AWS.

```bash
docker compose down
```

---

## 📤 Bước 3 — Đẩy image lên ECR

ECS **không đọc được image trên máy bạn**. Phải đẩy lên registry trước — dùng ECR.

### 3.1. Tạo 2 ECR repository

```bash
source ~/aws-fnb/env.sh

aws ecr create-repository --repository-name $PROJECT/order-service   >/dev/null
aws ecr create-repository --repository-name $PROJECT/product-service >/dev/null

save ECR_BASE "$ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com"
save ORDER_IMAGE   "$ECR_BASE/$PROJECT/order-service:v1"
save PRODUCT_IMAGE "$ECR_BASE/$PROJECT/product-service:v1"
```

### 3.2. Login Docker vào ECR

```bash
aws ecr get-login-password --region $AWS_REGION \
  | docker login --username AWS --password-stdin $ECR_BASE
```

### 3.3. Build đúng kiến trúc và push

```bash
cd ~/aws-fnb

docker buildx build --platform linux/amd64 -t $ORDER_IMAGE   ./order-service   --push
docker buildx build --platform linux/amd64 -t $PRODUCT_IMAGE ./product-service --push
```

> 🚨 **Gotcha lớn nhất với máy Mac M1/M2/M3.** Nếu bạn build bằng `docker build` thường, image sẽ là
> **arm64**. Fargate mặc định chạy **X86_64**. Task sẽ start rồi chết ngay, và lỗi trong CloudWatch là:
> ```
> exec /server: exec format error
> ```
> Lỗi này **không** nói gì về kiến trúc CPU, nên rất dễ đi lạc hướng cả buổi.
> Bắt buộc dùng `--platform linux/amd64` (hoặc set `cpuArchitecture: ARM64` trong task definition).

Verify image đã lên:

```bash
aws ecr describe-images --repository-name $PROJECT/order-service \
    --query 'imageDetails[].{Tag:imageTags[0],Pushed:imagePushedAt,MB:imageSizeInBytes}' --output table
```

---

## 🔐 Bước 4 — IAM role, Security Group, Log group

### 4.1. ECS Task Execution Role

Role này để **ECS agent** kéo image từ ECR và ghi log — không phải quyền của code bạn.

```bash
cat > ecs-trust-policy.json <<'EOF'
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Service": "ecs-tasks.amazonaws.com" },
      "Action": "sts:AssumeRole"
    }
  ]
}
EOF

aws iam create-role \
    --role-name ecsTaskExecutionRole-$PROJECT \
    --assume-role-policy-document file://ecs-trust-policy.json >/dev/null

aws iam attach-role-policy \
    --role-name ecsTaskExecutionRole-$PROJECT \
    --policy-arn arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy

save EXEC_ROLE_ARN "$(aws iam get-role --role-name ecsTaskExecutionRole-$PROJECT \
    --query Role.Arn --output text)"
```

> ⏳ Đợi ~10-15 giây cho IAM propagate trước khi tạo ECS service, nếu không sẽ gặp
> `ECS was unable to assume the configured role`.

### 4.2. Log group

```bash
aws logs create-log-group --log-group-name /ecs/$PROJECT/order-service
aws logs create-log-group --log-group-name /ecs/$PROJECT/product-service

aws logs put-retention-policy --log-group-name /ecs/$PROJECT/order-service   --retention-in-days 7
aws logs put-retention-policy --log-group-name /ecs/$PROJECT/product-service --retention-in-days 7
```

> ⚠️ **Phải tạo log group thủ công.** `AmazonECSTaskExecutionRolePolicy` có `logs:CreateLogStream`
> và `logs:PutLogEvents` nhưng **không có `logs:CreateLogGroup`**. Nếu log group chưa tồn tại,
> task sẽ fail khi start với `ResourceInitializationError: failed to validate logger args`.

### 4.3. Security Group cho ECS task

Giai đoạn đầu (Bước 5) chưa có ALB, bạn sẽ gọi thẳng vào task nên cần mở port từ IP của mình.

```bash
save ECS_SG "$(aws ec2 create-security-group \
    --group-name $PROJECT-ecs-sg \
    --description "ECS tasks" \
    --vpc-id $VPC_ID \
    --query GroupId --output text)"

save MY_IP "$(curl -s https://checkip.amazonaws.com)"

aws ec2 authorize-security-group-ingress --group-id $ECS_SG \
    --protocol tcp --port 8080 --cidr $MY_IP/32
aws ec2 authorize-security-group-ingress --group-id $ECS_SG \
    --protocol tcp --port 8081 --cidr $MY_IP/32
```

---

## 🚀 Bước 5 (Phase 3) — ECS Fargate, chưa có ALB

```
Internet ──> ECS Task (public IP) ──> Order Service
```

### 5.1. Tạo cluster

```bash
aws ecs create-cluster --cluster-name $PROJECT-cluster \
    --settings name=containerInsights,value=enabled >/dev/null

save CLUSTER "$PROJECT-cluster"
```

### 5.2. Task Definition cho Order Service

```bash
cat > order-taskdef.json <<EOF
{
  "family": "$PROJECT-order",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "256",
  "memory": "512",
  "executionRoleArn": "$EXEC_ROLE_ARN",
  "runtimePlatform": {
    "cpuArchitecture": "X86_64",
    "operatingSystemFamily": "LINUX"
  },
  "containerDefinitions": [
    {
      "name": "order",
      "image": "$ORDER_IMAGE",
      "essential": true,
      "portMappings": [
        { "containerPort": 8080, "protocol": "tcp" }
      ],
      "environment": [
        { "name": "PORT", "value": "8080" }
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/ecs/$PROJECT/order-service",
          "awslogs-region": "$AWS_REGION",
          "awslogs-stream-prefix": "ecs"
        }
      }
    }
  ]
}
EOF

aws ecs register-task-definition --cli-input-json file://order-taskdef.json \
    --query 'taskDefinition.taskDefinitionArn' --output text
```

> **Lưu ý heredoc:** ở đây dùng `<<EOF` (KHÔNG quote) để `$EXEC_ROLE_ARN`, `$ORDER_IMAGE` được thay giá trị thật.
> Các heredoc policy JSON ở trên dùng `<<'EOF'` (có quote) vì không cần thay biến.

### 5.3. Tạo ECS Service với 1 task

```bash
aws ecs create-service \
    --cluster $CLUSTER \
    --service-name order-service \
    --task-definition $PROJECT-order \
    --desired-count 1 \
    --launch-type FARGATE \
    --network-configuration "awsvpcConfiguration={subnets=[$SUBNETS],securityGroups=[$ECS_SG],assignPublicIp=ENABLED}" \
    >/dev/null

aws ecs wait services-stable --cluster $CLUSTER --services order-service
echo "service stable"
```

> `assignPublicIp=ENABLED` là **bắt buộc** khi task nằm trong public subnet và cần kéo image từ ECR.
> Không có public IP và cũng không có NAT Gateway → task treo ở `PENDING` rồi fail với
> `CannotPullContainerError: ... i/o timeout`. Đây là lỗi kinh điển số 1 của ECS Fargate.

### 5.4. Lấy public IP của task và test trực tiếp

```bash
get_task_ips() {
  aws ecs list-tasks --cluster $CLUSTER --service-name $1 --query 'taskArns[]' --output text \
  | xargs -n1 -I{} aws ecs describe-tasks --cluster $CLUSTER --tasks {} \
      --query 'tasks[0].attachments[0].details[?name==`networkInterfaceId`].value' --output text \
  | xargs -n1 -I{} aws ec2 describe-network-interfaces --network-interface-ids {} \
      --query 'NetworkInterfaces[0].Association.PublicIp' --output text
}

cat >> ~/aws-fnb/env.sh <<'EOF'

get_task_ips() {
  aws ecs list-tasks --cluster $CLUSTER --service-name $1 --query 'taskArns[]' --output text \
  | xargs -n1 -I{} aws ecs describe-tasks --cluster $CLUSTER --tasks {} \
      --query 'tasks[0].attachments[0].details[?name==`networkInterfaceId`].value' --output text \
  | xargs -n1 -I{} aws ec2 describe-network-interfaces --network-interface-ids {} \
      --query 'NetworkInterfaces[0].Association.PublicIp' --output text
}
EOF

get_task_ips order-service
```

```bash
IP=$(get_task_ips order-service | head -1)
curl -s http://$IP:8080/orders | jq
curl -s http://$IP:8080/health | jq
```

Xem log:

```bash
aws logs tail /ecs/$PROJECT/order-service --follow
```

### ✅ Checkpoint Phase 3

```
ECS Cluster → Task Definition → Task → Container
```

* **Task Definition** = bản thiết kế (image nào, CPU/RAM bao nhiêu, port nào, log đi đâu). Bất biến, có version.
* **Task** = một lần chạy thật của bản thiết kế đó. Có IP riêng (vì `awsvpc`), có vòng đời riêng.
* **Service** = thứ giữ cho luôn có đủ N task đang chạy. Task chết → service tạo task mới.

---

## 📈 Bước 6 (Phase 4) — Scale lên 3 task, và gặp vấn đề

```bash
aws ecs update-service --cluster $CLUSTER --service order-service --desired-count 3 >/dev/null
aws ecs wait services-stable --cluster $CLUSTER --services order-service

get_task_ips order-service
```

Bạn sẽ thấy 3 IP khác nhau, ví dụ:

```
13.212.44.101
54.169.88.23
18.142.7.190
```

Test từng cái:

```bash
for ip in $(get_task_ips order-service); do
  echo -n "$ip -> "
  curl -s http://$ip:8080/orders | jq -r .instance
done
```

### 🤔 Vấn đề

```
Client gọi task nào?
```

Các cách sai và lý do sai:

| Cách | Vì sao hỏng |
|---|---|
| Hardcode 3 IP vào client | Task restart là đổi IP. Scale lên 5 là client không biết. |
| Client tự random 1 trong 3 | Task chết thì client vẫn gọi vào IP chết. Không ai biết task nào healthy. |
| Dùng DNS round-robin | DNS bị cache ở client, không rút được node chết ra kịp. |

Cả 3 đều sai ở cùng một chỗ: **client không nên biết backend có bao nhiêu instance.**
Cần một thứ đứng giữa, biết danh sách instance, biết cái nào còn sống, và chia traffic.

→ Đó chính là **Load Balancer**.

---

## ⚖️ Bước 7 (Phase 5) — ALB

```
Internet ──> ALB ──> ECS #1 / #2 / #3
```

### 7.1. Security Group cho ALB

```bash
save ALB_SG "$(aws ec2 create-security-group \
    --group-name $PROJECT-alb-sg \
    --description "ALB public" \
    --vpc-id $VPC_ID \
    --query GroupId --output text)"

aws ec2 authorize-security-group-ingress --group-id $ALB_SG \
    --protocol tcp --port 80 --cidr 0.0.0.0/0
```

Cho phép ALB gọi vào ECS task:

```bash
aws ec2 authorize-security-group-ingress --group-id $ECS_SG \
    --protocol tcp --port 8080 --source-group $ALB_SG
aws ec2 authorize-security-group-ingress --group-id $ECS_SG \
    --protocol tcp --port 8081 --source-group $ALB_SG
```

> **`--source-group` chứ không phải `--cidr`.** ECS task đổi IP liên tục, không thể whitelist IP.
> Cho phép theo **security group** nghĩa là "bất cứ thứ gì mang SG của ALB đều vào được", bất kể IP.
> Đây là cách duy nhất đúng với hạ tầng động.

### 7.2. Tạo Application Load Balancer

```bash
save ALB_ARN "$(aws elbv2 create-load-balancer \
    --name $PROJECT-alb \
    --type application \
    --scheme internet-facing \
    --subnets $(echo $SUBNETS | tr ',' ' ') \
    --security-groups $ALB_SG \
    --query 'LoadBalancers[0].LoadBalancerArn' --output text)"

aws elbv2 wait load-balancer-available --load-balancer-arns $ALB_ARN

save ALB_DNS "$(aws elbv2 describe-load-balancers --load-balancer-arns $ALB_ARN \
    --query 'LoadBalancers[0].DNSName' --output text)"

echo "ALB: http://$ALB_DNS"
```

### 7.3. Target Group cho Order Service

```bash
save ORDER_TG "$(aws elbv2 create-target-group \
    --name $PROJECT-order-tg \
    --protocol HTTP --port 8080 \
    --vpc-id $VPC_ID \
    --target-type ip \
    --health-check-protocol HTTP \
    --health-check-path /health \
    --health-check-interval-seconds 15 \
    --health-check-timeout-seconds 5 \
    --healthy-threshold-count 2 \
    --unhealthy-threshold-count 2 \
    --matcher HttpCode=200 \
    --query 'TargetGroups[0].TargetGroupArn' --output text)"
```

> 🚨 **`--target-type ip` là bắt buộc với Fargate.** Mặc định là `instance`, dùng cho EC2.
> Fargate task dùng network mode `awsvpc` — mỗi task là một ENI với IP riêng, không gắn vào EC2 instance nào cả.
> Chọn nhầm `instance` thì ECS sẽ từ chối attach service với lỗi
> `The target group ... does not have an associated load balancer` hoặc target không bao giờ register được.

Giảm thời gian rút target chết ra (mặc định 300 giây, quá lâu để học):

```bash
aws elbv2 modify-target-group-attributes \
    --target-group-arn $ORDER_TG \
    --attributes Key=deregistration_delay.timeout_seconds,Value=15
```

### 7.4. Listener :80

```bash
save LISTENER_ARN "$(aws elbv2 create-listener \
    --load-balancer-arn $ALB_ARN \
    --protocol HTTP --port 80 \
    --default-actions Type=forward,TargetGroupArn=$ORDER_TG \
    --query 'Listeners[0].ListenerArn' --output text)"
```

Hiện tại:

```
ALB
 └── Listener :80  ──(default)──> Order Target Group
```

### 7.5. Gắn ECS Service vào Target Group

ECS service hiện tại được tạo **không có** load balancer. Cách sạch nhất là xoá và tạo lại.

```bash
aws ecs delete-service --cluster $CLUSTER --service order-service --force >/dev/null
aws ecs wait services-inactive --cluster $CLUSTER --services order-service

aws ecs create-service \
    --cluster $CLUSTER \
    --service-name order-service \
    --task-definition $PROJECT-order \
    --desired-count 3 \
    --launch-type FARGATE \
    --network-configuration "awsvpcConfiguration={subnets=[$SUBNETS],securityGroups=[$ECS_SG],assignPublicIp=ENABLED}" \
    --load-balancers "targetGroupArn=$ORDER_TG,containerName=order,containerPort=8080" \
    --health-check-grace-period-seconds 30 \
    >/dev/null

aws ecs wait services-stable --cluster $CLUSTER --services order-service
```

> `--health-check-grace-period-seconds 30`: ECS bỏ qua kết quả health check của ALB trong 30 giây đầu
> sau khi task start. Thiếu tham số này, app khởi động chậm sẽ bị ALB đánh unhealthy → ECS kill task
> → task mới start → lại bị kill. **Vòng lặp kill vô tận**, service không bao giờ `stable`.

Xem target đã register chưa:

```bash
aws elbv2 describe-target-health --target-group-arn $ORDER_TG \
    --query 'TargetHealthDescriptions[].{IP:Target.Id,Port:Target.Port,State:TargetHealth.State,Reason:TargetHealth.Reason}' \
    --output table
```

Chờ đến khi cả 3 target ở trạng thái `healthy`.

### 7.6. Test load balancing

```bash
curl -s http://$ALB_DNS/orders | jq
```

Gọi 12 lần và đếm xem mỗi task nhận bao nhiêu request:

```bash
for i in $(seq 1 12); do
  curl -s http://$ALB_DNS/orders | jq -r .instance
done | sort | uniq -c
```

Kết quả mong đợi — traffic trải đều:

```
   4 ip-10-0-1-23.ap-southeast-1.compute.internal
   4 ip-10-0-2-87.ap-southeast-1.compute.internal
   4 ip-10-0-3-11.ap-southeast-1.compute.internal
```

### 7.7. Test Health Check — cho một task chết lâm sàng

Gọi `/admin/toggle-health` cho đến khi trúng task bạn muốn hạ:

```bash
curl -s -X POST http://$ALB_DNS/admin/toggle-health | jq
```

Ghi lại `instance` vừa bị hạ. Sau ~30 giây (2 lần health check fail × 15s):

```bash
aws elbv2 describe-target-health --target-group-arn $ORDER_TG \
    --query 'TargetHealthDescriptions[].{IP:Target.Id,State:TargetHealth.State,Reason:TargetHealth.Reason}' \
    --output table
```

Một target chuyển sang `unhealthy` với reason `Target.ResponseCodeMismatch`.

Giờ gọi lại 12 lần:

```bash
for i in $(seq 1 12); do
  curl -s http://$ALB_DNS/orders | jq -r .instance
done | sort | uniq -c
```

→ Chỉ còn **2 instance** trong kết quả. **Không có request nào lỗi.** ALB tự rút target hỏng ra.

Bật lại:

```bash
curl -s -X POST http://$ALB_DNS/admin/toggle-health | jq   # lặp đến khi trúng đúng instance đó
```

### ✅ Checkpoint Phase 5

```
ALB ──> Listener ──> Rule ──> Target Group ──> Target (IP của task)
                                    ↑
                              Health Check
```

* **Listener**: cổng + protocol ALB lắng nghe (`:80 HTTP`).
* **Rule**: điều kiện định tuyến trên listener (path, host, header...). Mỗi listener có 1 default rule.
* **Target Group**: một nhóm backend cùng loại + cấu hình health check cho nhóm đó.
* **Health Check**: thứ quyết định target nào được nhận traffic. **Đây là giá trị cốt lõi của LB.**

Không có health check thì LB chỉ là bộ chia vòng tròn ngu ngốc, vẫn đẩy request vào máy chết.

---

## 🔀 Bước 8 (Phase 6) — ALB Path-based Routing

```
                    ALB :80
                       │
          ┌────────────┴────────────┐
          ▼                         ▼
     /orders*                  /products*
          │                         │
          ▼                         ▼
    Order TG                  Product TG
   (ECS #1 #2 #3)              (ECS #1 #2)
```

### 8.1. Đưa Product Service lên ECS

```bash
cat > product-taskdef.json <<EOF
{
  "family": "$PROJECT-product",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "256",
  "memory": "512",
  "executionRoleArn": "$EXEC_ROLE_ARN",
  "runtimePlatform": {
    "cpuArchitecture": "X86_64",
    "operatingSystemFamily": "LINUX"
  },
  "containerDefinitions": [
    {
      "name": "product",
      "image": "$PRODUCT_IMAGE",
      "essential": true,
      "portMappings": [
        { "containerPort": 8081, "protocol": "tcp" }
      ],
      "environment": [
        { "name": "PORT", "value": "8081" }
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/ecs/$PROJECT/product-service",
          "awslogs-region": "$AWS_REGION",
          "awslogs-stream-prefix": "ecs"
        }
      }
    }
  ]
}
EOF

aws ecs register-task-definition --cli-input-json file://product-taskdef.json \
    --query 'taskDefinition.taskDefinitionArn' --output text
```

### 8.2. Target Group cho Product

```bash
save PRODUCT_TG "$(aws elbv2 create-target-group \
    --name $PROJECT-product-tg \
    --protocol HTTP --port 8081 \
    --vpc-id $VPC_ID \
    --target-type ip \
    --health-check-protocol HTTP \
    --health-check-path /health \
    --health-check-interval-seconds 15 \
    --health-check-timeout-seconds 5 \
    --healthy-threshold-count 2 \
    --unhealthy-threshold-count 2 \
    --matcher HttpCode=200 \
    --query 'TargetGroups[0].TargetGroupArn' --output text)"

aws elbv2 modify-target-group-attributes \
    --target-group-arn $PRODUCT_TG \
    --attributes Key=deregistration_delay.timeout_seconds,Value=15
```

### 8.3. ECS Service cho Product, gắn thẳng vào TG

```bash
aws ecs create-service \
    --cluster $CLUSTER \
    --service-name product-service \
    --task-definition $PROJECT-product \
    --desired-count 2 \
    --launch-type FARGATE \
    --network-configuration "awsvpcConfiguration={subnets=[$SUBNETS],securityGroups=[$ECS_SG],assignPublicIp=ENABLED}" \
    --load-balancers "targetGroupArn=$PRODUCT_TG,containerName=product,containerPort=8081" \
    --health-check-grace-period-seconds 30 \
    >/dev/null

aws ecs wait services-stable --cluster $CLUSTER --services product-service
```

### 8.4. Tạo Listener Rules

```bash
aws elbv2 create-rule \
    --listener-arn $LISTENER_ARN \
    --priority 10 \
    --conditions 'Field=path-pattern,Values=["/orders","/orders/*"]' \
    --actions Type=forward,TargetGroupArn=$ORDER_TG \
    --query 'Rules[0].RuleArn' --output text

aws elbv2 create-rule \
    --listener-arn $LISTENER_ARN \
    --priority 20 \
    --conditions 'Field=path-pattern,Values=["/products","/products/*"]' \
    --actions Type=forward,TargetGroupArn=$PRODUCT_TG \
    --query 'Rules[0].RuleArn' --output text
```

> 🚨 **Gotcha path-pattern:** `/orders/*` **KHÔNG match** `/orders`. Dấu `/` trong pattern là ký tự thật.
> Nếu chỉ khai `/orders/*`, thì `GET /orders` sẽ rơi xuống **default action** của listener —
> có thể trúng nhầm target group khác và bạn sẽ debug rất lâu vì `/orders/1` chạy ngon còn `/orders` thì không.
> Luôn khai **cả hai**: `["/orders","/orders/*"]`.

Đổi default action thành 404 để mọi định tuyến đều phải đi qua rule tường minh:

```bash
aws elbv2 modify-listener \
    --listener-arn $LISTENER_ARN \
    --default-actions '[{
      "Type":"fixed-response",
      "FixedResponseConfig":{
        "StatusCode":"404",
        "ContentType":"application/json",
        "MessageBody":"{\"error\":\"no ALB rule matched\"}"
      }
    }]' >/dev/null
```

> Đây là mẹo debug cực hữu ích: khi thấy `{"error":"no ALB rule matched"}` bạn biết ngay
> **request đã tới ALB nhưng không rule nào khớp** — chứ không phải app trả 404.
> Nếu để default action forward vào Order TG, hai trường hợp này trông y hệt nhau.

Xem toàn bộ rule theo thứ tự ưu tiên:

```bash
aws elbv2 describe-rules --listener-arn $LISTENER_ARN \
    --query 'Rules[].{Priority:Priority,Path:Conditions[0].Values,Action:Actions[0].Type}' \
    --output table
```

> **Priority quyết định tất cả.** ALB duyệt rule từ priority nhỏ đến lớn và **dừng ở rule khớp đầu tiên**.
> `default` luôn được xét cuối cùng. Nếu bạn đặt một rule `/*` ở priority 5, mọi rule phía sau thành vô nghĩa.

### 8.5. Test routing

```bash
curl -s http://$ALB_DNS/orders     | jq
curl -s http://$ALB_DNS/orders/1   | jq
curl -s http://$ALB_DNS/products   | jq
curl -s http://$ALB_DNS/products/2 | jq
curl -s http://$ALB_DNS/unknown    | jq
```

Kiểm tra request thực sự đi đúng service:

```bash
echo "--- orders"
for i in $(seq 1 6); do curl -s http://$ALB_DNS/orders | jq -r .instance; done | sort | uniq -c
echo "--- products"
for i in $(seq 1 6); do curl -s http://$ALB_DNS/products | jq -r .instance; done | sort | uniq -c
```

→ Hai nhóm instance **hoàn toàn khác nhau**. Đó là bằng chứng path-based routing hoạt động.

### ✅ Checkpoint Phase 6

```
GET /orders    ──> ALB ──> Rule p10 ──> Order TG   ──> Order Service
GET /products  ──> ALB ──> Rule p20 ──> Product TG ──> Product Service
GET /unknown   ──> ALB ──> default  ──> 404
```

ALB đã làm xong việc của nó: **routing theo path + load balancing + health check trong VPC.**

---

## 🌐 Bước 9 (Phase 7 + 8) — API Gateway (HTTP API)

```
Client ──> API Gateway ──> ALB ──> ECS
```

Dùng **HTTP API** (apigatewayv2), không dùng REST API v1 — rẻ hơn ~70%, nhanh hơn, và JWT authorizer có sẵn.

### 9.1. Tạo HTTP API

```bash
save API_ID "$(aws apigatewayv2 create-api \
    --name $PROJECT-api \
    --protocol-type HTTP \
    --query ApiId --output text)"
```

### 9.2. Tạo Integration (HTTP_PROXY → ALB)

Mỗi URI backend khác nhau cần một integration. Ta cần 4 cái:

```bash
mk_integration() {
  aws apigatewayv2 create-integration \
    --api-id $API_ID \
    --integration-type HTTP_PROXY \
    --integration-method ANY \
    --integration-uri "$1" \
    --payload-format-version 1.0 \
    --connection-type INTERNET \
    --query IntegrationId --output text
}

save INT_ORDERS     "$(mk_integration http://$ALB_DNS/orders)"
save INT_ORDER_ID   "$(mk_integration http://$ALB_DNS/orders/{id})"
save INT_PRODUCTS   "$(mk_integration http://$ALB_DNS/products)"
save INT_PRODUCT_ID "$(mk_integration http://$ALB_DNS/products/{id})"
```

> **`{id}` trong integration URI phải trùng tên với path parameter của route.** API Gateway thay
> `{id}` bằng giá trị bắt được từ route `GET /orders/{id}`. Đặt tên lệch (`{orderId}` ở route
> nhưng `{id}` ở integration) → API Gateway trả **500 Internal Server Error** và log ghi
> `Invalid mapping expression specified`.

> **`--payload-format-version 1.0`**: với `HTTP_PROXY` thì giá trị này bắt buộc là `1.0`.
> `2.0` chỉ dùng cho `AWS_PROXY` (Lambda).

### 9.3. Tạo Routes

```bash
mk_route() {
  aws apigatewayv2 create-route \
    --api-id $API_ID \
    --route-key "$1" \
    --target "integrations/$2" \
    --query RouteId --output text
}

save R_GET_ORDERS    "$(mk_route 'GET /orders'           $INT_ORDERS)"
save R_POST_ORDERS   "$(mk_route 'POST /orders'          $INT_ORDERS)"
save R_GET_ORDER     "$(mk_route 'GET /orders/{id}'      $INT_ORDER_ID)"
save R_DEL_ORDER     "$(mk_route 'DELETE /orders/{id}'   $INT_ORDER_ID)"
save R_GET_PRODUCTS  "$(mk_route 'GET /products'         $INT_PRODUCTS)"
save R_POST_PRODUCTS "$(mk_route 'POST /products'        $INT_PRODUCTS)"
save R_GET_PRODUCT   "$(mk_route 'GET /products/{id}'    $INT_PRODUCT_ID)"
```

> **Tại sao khai từng route thay vì một `ANY /{proxy+}`?** Catch-all thì nhanh hơn, nhưng bạn mất:
> throttling riêng cho từng route (Bước 12), authorizer riêng cho từng route (Bước 11),
> và metric tách theo route. Route tường minh chính là điểm khác biệt lớn nhất
> giữa API Gateway và một reverse proxy thường.

### 9.4. Tạo Stage `$default` với auto-deploy

```bash
aws apigatewayv2 create-stage \
    --api-id $API_ID \
    --stage-name '$default' \
    --auto-deploy >/dev/null

save API_URL "$(aws apigatewayv2 get-api --api-id $API_ID \
    --query ApiEndpoint --output text)"

echo "API: $API_URL"
```

> Stage tên `$default` cho URL sạch: `https://abc123.execute-api.<region>.amazonaws.com/orders`.
> Stage tên khác (ví dụ `prod`) thì URL thành `.../prod/orders` và **API Gateway sẽ strip `/prod`
> trước khi gọi backend** — dễ nhầm khi debug.
> `--auto-deploy` để mọi thay đổi route/integration tự lên ngay, khỏi phải `create-deployment` thủ công.

### 9.5. Test qua API Gateway

```bash
curl -s $API_URL/orders     | jq
curl -s $API_URL/orders/1   | jq
curl -s $API_URL/products   | jq
curl -s $API_URL/products/3 | jq

curl -s -X POST $API_URL/orders \
  -H 'Content-Type: application/json' \
  -d '{"product":"Sinh to bo","quantity":1}' | jq

curl -s -X DELETE $API_URL/orders/2 | jq
```

Route chưa khai thì API Gateway chặn ngay, không chạm tới ALB:

```bash
curl -i -s $API_URL/unknown | head -5
# HTTP/2 404
# {"message":"Not Found"}
```

So sánh với `curl -s http://$ALB_DNS/unknown` → `{"error":"no ALB rule matched"}`.
**Hai lớp 404 khác nhau, phân biệt được là đang debug đúng chỗ.**

### ✅ Checkpoint Phase 7 + 8

```
Client
  │ GET /orders/1
  ▼
API Gateway  — khớp route "GET /orders/{id}", bắt id=1
  │
  ▼
Integration  — thay {id} vào URI http://ALB/orders/{id}
  │
  ▼
ALB          — rule /orders/* → Order TG
  │
  ▼
ECS task     — handler getOrder, r.PathValue("id") == "1"
```

**API Gateway không phải load balancer.** Nó không biết có bao nhiêu ECS task, không health check task nào.
Nó chỉ biết đúng một backend: cái DNS name của ALB. Việc chia traffic vẫn 100% là của ALB.

Ngược lại, ALB không biết gì về route key, authorizer, throttling hay Lambda.
**Hai thằng làm hai việc khác nhau nên đứng cạnh nhau được.**

---

## ⚡ Bước 10 (Phase 9) — API Gateway → Lambda

```
              API Gateway
              /         \
   HTTP_PROXY/           \AWS_PROXY
            ▼             ▼
           ALB          Lambda
            │         /system/info
            ▼
           ECS
```

### 10.1. `system-info-lambda/main.go`

```go
package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

type systemInfo struct {
	Service     string `json:"service"`
	Version     string `json:"version"`
	Environment string `json:"environment"`
	Region      string `json:"region"`
	RequestID   string `json:"requestId"`
	Source      string `json:"source"`
}

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	info := systemInfo{
		Service:     "order-system",
		Version:     "1.0.0",
		Environment: os.Getenv("ENVIRONMENT"),
		Region:      os.Getenv("AWS_REGION"),
		RequestID:   req.RequestContext.RequestID,
		Source:      "lambda",
	}

	body, _ := json.Marshal(info)

	return events.APIGatewayV2HTTPResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(body),
	}, nil
}

func main() {
	lambda.Start(handler)
}
```

**`system-info-lambda/go.mod`:**
```
module system-info-lambda

go 1.22

require github.com/aws/aws-lambda-go v1.47.0
```

### 10.2. Build và deploy

```bash
cd ~/aws-fnb/system-info-lambda
go mod tidy

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bootstrap main.go
zip -j function.zip bootstrap
```

> Lambda Go dùng custom runtime `provided.al2023`, và binary **bắt buộc tên `bootstrap`**.
> Tên khác → `Runtime.InvalidEntrypoint`.

IAM role cho Lambda:

```bash
cd ~/aws-fnb

cat > lambda-trust-policy.json <<'EOF'
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
EOF

aws iam create-role \
    --role-name lambdaRole-$PROJECT \
    --assume-role-policy-document file://lambda-trust-policy.json >/dev/null

aws iam attach-role-policy \
    --role-name lambdaRole-$PROJECT \
    --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole

save LAMBDA_ROLE_ARN "$(aws iam get-role --role-name lambdaRole-$PROJECT \
    --query Role.Arn --output text)"

sleep 15
```

Tạo function:

```bash
save SYSINFO_LAMBDA_ARN "$(aws lambda create-function \
    --function-name $PROJECT-system-info \
    --runtime provided.al2023 \
    --role $LAMBDA_ROLE_ARN \
    --handler bootstrap \
    --zip-file fileb://system-info-lambda/function.zip \
    --timeout 10 \
    --memory-size 128 \
    --environment 'Variables={ENVIRONMENT=production}' \
    --query FunctionArn --output text)"
```

> `AWS_REGION` là biến môi trường Lambda tự set, **không được** khai lại trong `--environment`
> (sẽ bị lỗi `Environment variable ... is reserved`). Code đọc thẳng `os.Getenv("AWS_REGION")` là có.

### 10.3. Nối Lambda vào API Gateway

```bash
save INT_LAMBDA "$(aws apigatewayv2 create-integration \
    --api-id $API_ID \
    --integration-type AWS_PROXY \
    --integration-uri $SYSINFO_LAMBDA_ARN \
    --payload-format-version 2.0 \
    --query IntegrationId --output text)"

save R_SYSINFO "$(aws apigatewayv2 create-route \
    --api-id $API_ID \
    --route-key 'GET /system/info' \
    --target integrations/$INT_LAMBDA \
    --query RouteId --output text)"
```

**Cấp quyền cho API Gateway gọi Lambda** — thiếu bước này là 500, và lỗi im lặng phía client:

```bash
aws lambda add-permission \
    --function-name $PROJECT-system-info \
    --statement-id apigw-invoke \
    --action lambda:InvokeFunction \
    --principal apigateway.amazonaws.com \
    --source-arn "arn:aws:execute-api:$AWS_REGION:$ACCOUNT_ID:$API_ID/*/*/system/info" \
    >/dev/null
```

> 🚨 Đây là **bước hay quên nhất** của cả project. Không có resource policy này, API Gateway trả
> `{"message":"Internal Server Error"}` mà **không nói gì về permission**. Muốn thấy lý do thật
> phải bật access log (Bước 13) và đọc `$context.integrationErrorMessage`.

> **`--integration-type AWS_PROXY` chứ không phải `--integration-type LAMBDA_PROXY`** —
> tên gọi trong doc là "Lambda proxy integration" nhưng giá trị API là `AWS_PROXY`.

### 10.4. Test

```bash
curl -s $API_URL/system/info | jq
```

```json
{
  "service": "order-system",
  "version": "1.0.0",
  "environment": "production",
  "region": "ap-southeast-1",
  "requestId": "Mv8xxJd1SQ0EJ...",
  "source": "lambda"
}
```

Xem log:

```bash
aws logs tail /aws/lambda/$PROJECT-system-info --follow
```

### ✅ Checkpoint Phase 9

```
API Gateway
 ├── HTTP_PROXY   → ALB      → ECS (container chạy 24/7, có state trong RAM, scale bằng task)
 └── AWS_PROXY    → Lambda   (chạy khi có request, không state, scale tự động theo concurrency)
```

Cùng một API Gateway, **cùng một domain, cùng một JWT authorizer, cùng một throttling policy** —
nhưng backend hoàn toàn khác loại. Đó là ý nghĩa của **Integration**: nó tách
*"API trông như thế nào với client"* khỏi *"ai thực sự xử lý request"*.

```bash
curl -s $API_URL/orders      | jq -r '.instance'   # ECS task
curl -s $API_URL/system/info | jq -r '.source'     # lambda
```

Hai lệnh, một domain, hai thế giới backend khác nhau.

---

## 🔑 Bước 11 (Phase 10) — JWT Authentication

```
Client
  │ Authorization: Bearer eyJ...
  ▼
API Gateway ── JWT Authorizer ──> Cognito JWKS (kiểm chữ ký)
  │
  │ hợp lệ
  ▼
ALB ──> ECS
```

JWT authorizer của HTTP API cần một **OIDC issuer** có endpoint JWKS công khai để lấy public key.
Dùng **Cognito User Pool** — không phải viết code gì.

### 11.1. Hiểu JWT trước đã

Một JWT gồm 3 phần nối bằng dấu `.`:

```
eyJraWQiOiJ...    .    eyJzdWIiOiJ...    .    Nx8Kq2p...
   Header                  Payload             Signature
  (base64url)            (base64url)          (binary)
```

| Phần | Nội dung | Ai đọc |
|---|---|---|
| Header | `alg` (RS256), `kid` (key id) | API Gateway dùng `kid` để chọn đúng public key trong JWKS |
| Payload | `sub`, `exp`, `aud`/`client_id`, `iss`, custom claims | API Gateway validate `exp`/`aud`/`iss`; backend đọc `sub`, `role` |
| Signature | ký bằng **private key** của issuer | API Gateway verify bằng **public key** lấy từ JWKS |

Payload ví dụ:

```json
{
  "sub": "8a4b1c2d-...",
  "aud": "3h5k9l...",
  "iss": "https://cognito-idp.ap-southeast-1.amazonaws.com/ap-southeast-1_XxYyZz",
  "exp": 1780000000,
  "token_use": "id",
  "cognito:username": "nam"
}
```

> **Điểm quan trọng:** JWT **không được mã hoá**, chỉ được **ký**. Ai cũng decode và đọc payload được.
> Giá trị của nó là *không ai sửa được payload mà chữ ký vẫn đúng*.
> → **Không bao giờ để dữ liệu nhạy cảm trong JWT payload.**

### 11.2. Tạo Cognito User Pool

```bash
save POOL_ID "$(aws cognito-idp create-user-pool \
    --pool-name $PROJECT-pool \
    --policies 'PasswordPolicy={MinimumLength=8,RequireUppercase=true,RequireLowercase=true,RequireNumbers=true,RequireSymbols=false}' \
    --query 'UserPool.Id' --output text)"

save CLIENT_ID "$(aws cognito-idp create-user-pool-client \
    --user-pool-id $POOL_ID \
    --client-name $PROJECT-client \
    --no-generate-secret \
    --explicit-auth-flows ALLOW_USER_PASSWORD_AUTH ALLOW_REFRESH_TOKEN_AUTH \
    --query 'UserPoolClient.ClientId' --output text)"

save ISSUER "https://cognito-idp.$AWS_REGION.amazonaws.com/$POOL_ID"

echo "Issuer: $ISSUER"
curl -s $ISSUER/.well-known/openid-configuration | jq
curl -s $ISSUER/.well-known/jwks.json | jq '.keys[].kid'
```

> `--no-generate-secret` là bắt buộc cho client kiểu public (curl, SPA, mobile).
> Có secret thì `initiate-auth` sẽ đòi `SECRET_HASH` và bạn phải tự tính HMAC — thêm việc không cần thiết.

### 11.3. Tạo user và lấy token

```bash
aws cognito-idp admin-create-user \
    --user-pool-id $POOL_ID \
    --username nam \
    --message-action SUPPRESS \
    --user-attributes Name=email,Value=nam@example.com Name=email_verified,Value=true \
    >/dev/null

aws cognito-idp admin-set-user-password \
    --user-pool-id $POOL_ID \
    --username nam \
    --password 'Passw0rd123' \
    --permanent
```

```bash
get_token() {
  aws cognito-idp initiate-auth \
    --auth-flow USER_PASSWORD_AUTH \
    --client-id $CLIENT_ID \
    --auth-parameters USERNAME=nam,PASSWORD=Passw0rd123 \
    --query 'AuthenticationResult.IdToken' --output text
}

cat >> ~/aws-fnb/env.sh <<'EOF'

get_token() {
  aws cognito-idp initiate-auth \
    --auth-flow USER_PASSWORD_AUTH \
    --client-id $CLIENT_ID \
    --auth-parameters USERNAME=nam,PASSWORD=Passw0rd123 \
    --query 'AuthenticationResult.IdToken' --output text
}
EOF

TOKEN=$(get_token)
echo $TOKEN
```

Decode payload để nhìn tận mắt:

```bash
echo $TOKEN | cut -d. -f2 | base64 -d 2>/dev/null | jq
```

> 🚨 **IdToken hay AccessToken?** Đây là chỗ mất thời gian nhiều nhất của phase này.
> - **IdToken** có claim `aud` = Client ID → khớp thẳng với `Audience` của authorizer.
> - **AccessToken** của Cognito **không có `aud`**, nó dùng `client_id`. API Gateway có xử lý riêng
>   cho Cognito access token, nhưng nếu bạn cấu hình sai `Audience` thì lỗi trả về chỉ là
>   `401 {"message":"Unauthorized"}` — **không nói claim nào sai**.
>
> Trong bài này dùng **IdToken** cho chắc chắn.

### 11.4. Tạo JWT Authorizer

```bash
save AUTHORIZER_ID "$(aws apigatewayv2 create-authorizer \
    --api-id $API_ID \
    --authorizer-type JWT \
    --name $PROJECT-jwt \
    --identity-source '$request.header.Authorization' \
    --jwt-configuration "Audience=$CLIENT_ID,Issuer=$ISSUER" \
    --query AuthorizerId --output text)"
```

### 11.5. Gắn authorizer vào từng route

```bash
protect() {
  aws apigatewayv2 update-route \
    --api-id $API_ID \
    --route-id $1 \
    --authorization-type JWT \
    --authorizer-id $AUTHORIZER_ID >/dev/null
  echo "protected: $1"
}

protect $R_GET_ORDERS
protect $R_POST_ORDERS
protect $R_GET_ORDER
protect $R_DEL_ORDER
protect $R_GET_PRODUCTS
protect $R_POST_PRODUCTS
protect $R_GET_PRODUCT
```

Để `GET /system/info` **public** (không gắn authorizer) — để so sánh.

Xem lại toàn bộ:

```bash
aws apigatewayv2 get-routes --api-id $API_ID \
    --query 'Items[].{Route:RouteKey,Auth:AuthorizationType}' --output table
```

### 11.6. Test 3 tình huống

```bash
echo "--- Không có JWT"
curl -i -s $API_URL/orders | head -3

echo "--- JWT rác"
curl -i -s $API_URL/orders -H "Authorization: Bearer abc.def.ghi" | head -3

echo "--- JWT hợp lệ"
TOKEN=$(get_token)
curl -s $API_URL/orders -H "Authorization: Bearer $TOKEN" | jq

echo "--- Route public, không cần JWT"
curl -s $API_URL/system/info | jq
```

Kết quả mong đợi:

```
Không có JWT   → 401 {"message":"Unauthorized"}
JWT rác        → 401 {"message":"Unauthorized"}
JWT hợp lệ     → 200 {"instance":"ip-10-0-...","orders":[...]}
/system/info   → 200 (public)
```

Test token hết hạn (Cognito IdToken mặc định sống 60 phút):

```bash
echo $TOKEN | cut -d. -f2 | base64 -d 2>/dev/null | jq '.exp, (.exp - now | floor)'
```

### ✅ Checkpoint Phase 10

```
No JWT        → 401   (API Gateway chặn, ALB KHÔNG hề thấy request này)
Invalid JWT   → 401   (API Gateway chặn)
Expired JWT   → 401   (API Gateway chặn)
Valid JWT     → 200   (đi tiếp: ALB → ECS)
```

Điểm mấu chốt: **request bị chặn ở API Gateway không bao giờ chạm tới ALB hay ECS.**
Kiểm chứng bằng cách gọi 20 request không token rồi xem log ECS — không có dòng nào.

```bash
for i in $(seq 1 20); do curl -s -o /dev/null $API_URL/orders; done
aws logs tail /ecs/$PROJECT/order-service --since 1m | grep 'GET /orders' | wc -l   # → 0
```

> **Đây chính là lý do tồn tại của API Gateway.** Nếu auth nằm trong app code, 20 request rác đó
> vẫn tiêu CPU của ECS. Đặt auth ở edge nghĩa là **traffic rác chết trước khi vào hạ tầng của bạn.**

### 11.7. (Tuỳ chọn) Backend đọc claim từ JWT

API Gateway v2 payload 2.0 truyền claim xuống Lambda qua `requestContext.authorizer.jwt.claims`.
Với **HTTP_PROXY → ALB** thì claim **không tự đi kèm** — phải map thủ công thành header:

```bash
aws apigatewayv2 update-integration \
    --api-id $API_ID \
    --integration-id $INT_ORDERS \
    --request-parameters 'append:header.X-User-Sub=$context.authorizer.claims.sub' \
    >/dev/null
```

Rồi trong Go đọc `r.Header.Get("X-User-Sub")`.

> ⚠️ Nếu làm vậy, phải **chặn client tự set header đó** — nhưng `append:` chỉ *thêm vào* giá trị client gửi.
> Dùng `overwrite:header.X-User-Sub=...` để ghi đè hẳn. Đây là lỗ hổng impersonation kinh điển
> khi truyền identity qua header giữa gateway và backend.

---

## 🚦 Bước 12 (Phase 11) — Throttling / Rate Limiting

```
Client (spam)
   ↓
API Gateway ── Rate 5 req/s, Burst 10 ──> 429 cho phần vượt
   ↓ phần được qua
  ALB → ECS
```

### 12.1. Khái niệm: Rate vs Burst (token bucket)

```
Burst = 10   ← kích thước xô, số request "dồn" tối đa cho phép tại một thời điểm
Rate  = 5/s  ← tốc độ đổ token vào xô

Gửi 10 request cùng lúc  → cả 10 qua (xô đầy sẵn)
Gửi 30 request cùng lúc  → ~10 qua, ~20 bị 429
Gửi đều 5 req/s liên tục → qua hết, mãi mãi
```

**Rate** giới hạn thông lượng trung bình, **Burst** cho phép gai đột biến ngắn.
Chỉ có Rate mà không Burst thì mọi traffic thật (vốn luôn gợn sóng) đều bị cắt oan.

### 12.2. Set throttle mặc định cho cả stage

```bash
aws apigatewayv2 update-stage \
    --api-id $API_ID \
    --stage-name '$default' \
    --default-route-settings 'ThrottlingRateLimit=5,ThrottlingBurstLimit=10' \
    >/dev/null
```

### 12.3. Set throttle riêng cho một route

```bash
aws apigatewayv2 update-stage \
    --api-id $API_ID \
    --stage-name '$default' \
    --route-settings '{
      "GET /orders":    {"ThrottlingRateLimit": 2, "ThrottlingBurstLimit": 2},
      "POST /orders":   {"ThrottlingRateLimit": 1, "ThrottlingBurstLimit": 1},
      "GET /system/info": {"ThrottlingRateLimit": 50, "ThrottlingBurstLimit": 100}
    }' >/dev/null

aws apigatewayv2 get-stage --api-id $API_ID --stage-name '$default' \
    --query '{Default:DefaultRouteSettings,PerRoute:RouteSettings}' --output json | jq
```

> Key của `--route-settings` là **route key đầy đủ có khoảng trắng**: `"GET /orders"`, không phải `/orders`.
> Sai key thì lệnh **vẫn thành công** nhưng setting không áp vào đâu cả — fail hoàn toàn im lặng.

### 12.4. Test nhanh bằng curl

```bash
TOKEN=$(get_token)
for i in $(seq 1 20); do
  curl -s -o /dev/null -w "%{http_code} " $API_URL/orders -H "Authorization: Bearer $TOKEN"
done
echo
```

Mong đợi: vài `200` đầu rồi hàng loạt `429`.

```bash
curl -s $API_URL/orders -H "Authorization: Bearer $TOKEN" ; echo
# {"message":"Too Many Requests"}
```

### 12.5. Load test bằng k6

**`k6-test.js`:**

```javascript
import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

const ok = new Counter('status_2xx');
const throttled = new Counter('status_429');
const serverError = new Counter('status_5xx');

export const options = {
  stages: [
    { duration: '30s', target: 10 },
    { duration: '30s', target: 50 },
    { duration: '30s', target: 100 },
    { duration: '15s', target: 0 },
  ],
};

const API_URL = __ENV.API_URL;
const TOKEN = __ENV.TOKEN;

export default function () {
  const res = http.get(`${API_URL}/orders`, {
    headers: { Authorization: `Bearer ${TOKEN}` },
  });

  if (res.status >= 200 && res.status < 300) ok.add(1);
  else if (res.status === 429) throttled.add(1);
  else if (res.status >= 500) serverError.add(1);

  check(res, {
    'not 5xx': (r) => r.status < 500,
  });
}
```

Chạy:

```bash
export TOKEN=$(get_token)
k6 run -e API_URL=$API_URL -e TOKEN=$TOKEN k6-test.js
```

Quan sát trong output:

```
status_2xx ......: số request được phục vụ
status_429 ......: số request bị API Gateway chặn
status_5xx ......: PHẢI BẰNG 0
http_req_duration: p(95) nên ổn định, không tăng vọt
```

> **Kết luận quan trọng nhất của phase này:** với 100 VU, nếu `status_5xx = 0` và
> `http_req_duration p(95)` không tăng vọt, nghĩa là throttling đã **bảo vệ ECS thành công**.
> Traffic thừa chết ở API Gateway (429 — lỗi rẻ, xử lý ở edge) chứ không làm ECS sập (5xx — lỗi đắt).
>
> Kiểm chứng ngược: tăng throttle lên `ThrottlingRateLimit=10000` rồi chạy lại k6 —
> `429` về 0 nhưng latency ECS tăng và bắt đầu có `5xx`. **Đó là cái giá của việc bỏ throttling.**

Bỏ throttle chặt để làm bước sau cho thoải mái:

```bash
aws apigatewayv2 update-stage --api-id $API_ID --stage-name '$default' \
    --default-route-settings 'ThrottlingRateLimit=200,ThrottlingBurstLimit=400' \
    --route-settings '{}' >/dev/null
```

### 12.6. API Gateway throttling vs Application rate limiting

| | API Gateway throttling | App-level rate limiting |
|---|---|---|
| Chạy ở đâu | Edge, trước khi vào VPC | Trong code ECS |
| Bảo vệ được gì | Toàn bộ backend (ALB, ECS, Lambda) | Chỉ logic phía sau nó |
| Tiêu tài nguyên của bạn? | Không | Có — request đã tốn CPU/RAM để bị từ chối |
| Phân biệt theo user? | Không (HTTP API chỉ theo route/stage) | Có — theo user id, tenant, API key |
| Trạng thái | AWS lo | Bạn phải tự lo (Redis nếu nhiều instance) |

→ Chúng **bổ sung cho nhau**: gateway chặn lũ, app chặn theo từng user cụ thể.

---

## 📊 Bước 13 (Phase 12) — Monitoring

Mục tiêu duy nhất: **request lỗi thì biết lỗi ở API Gateway, ALB hay ECS.**

### 13.1. Bật Access Log cho API Gateway

```bash
aws logs create-log-group --log-group-name /aws/apigateway/$PROJECT
aws logs put-retention-policy --log-group-name /aws/apigateway/$PROJECT --retention-in-days 7

save APIGW_LOG_ARN "arn:aws:logs:$AWS_REGION:$ACCOUNT_ID:log-group:/aws/apigateway/$PROJECT"

aws apigatewayv2 update-stage \
    --api-id $API_ID \
    --stage-name '$default' \
    --access-log-settings "DestinationArn=$APIGW_LOG_ARN,Format={\"requestId\":\"\$context.requestId\",\"ip\":\"\$context.identity.sourceIp\",\"route\":\"\$context.routeKey\",\"status\":\"\$context.status\",\"integrationStatus\":\"\$context.integrationStatus\",\"integrationError\":\"\$context.integrationErrorMessage\",\"authorizerError\":\"\$context.authorizer.error\",\"latency\":\"\$context.responseLatency\",\"integrationLatency\":\"\$context.integrationLatency\"}" \
    >/dev/null
```

> Trong shell, `$context...` phải escape thành `\$context...` nếu dùng nháy kép,
> nếu không shell sẽ thay bằng chuỗi rỗng và log ra toàn `""`.

Ba field quan trọng nhất:

| Field | Ý nghĩa khi debug |
|---|---|
| `status` | Mã trả về client |
| `integrationStatus` | Mã **backend trả cho API Gateway**. Khác `status` → API Gateway tự sinh lỗi |
| `integrationErrorMessage` | Lý do thật khi backend không gọi được (thiếu permission, ALB timeout...) |

Đọc log:

```bash
curl -s $API_URL/orders -H "Authorization: Bearer $(get_token)" >/dev/null
aws logs tail /aws/apigateway/$PROJECT --since 5m --format short | tail -5 | jq -c
```

### 13.2. Metrics của 3 tầng

**API Gateway:**

```bash
metric_apigw() {
  aws cloudwatch get-metric-statistics \
    --namespace AWS/ApiGateway --metric-name $1 \
    --dimensions Name=ApiId,Value=$API_ID \
    --start-time "$(date -u -v-1H +%Y-%m-%dT%H:%M:%S)" \
    --end-time   "$(date -u +%Y-%m-%dT%H:%M:%S)" \
    --period 300 --statistics $2 \
    --query "sort_by(Datapoints,&Timestamp)[].{T:Timestamp,V:$2}" --output text
}

for m in Count 4xx 5xx; do echo "--- $m"; metric_apigw $m Sum; done
echo "--- Latency"; metric_apigw Latency Average
echo "--- IntegrationLatency"; metric_apigw IntegrationLatency Average
```

> **`Latency` - `IntegrationLatency` = thời gian API Gateway tự tiêu tốn** (auth, throttle, mapping).
> Chênh lệch lớn → vấn đề ở gateway. Gần bằng nhau → chậm là do backend.

**ALB:**

```bash
ALB_DIM=$(aws elbv2 describe-load-balancers --load-balancer-arns $ALB_ARN \
  --query 'LoadBalancers[0].LoadBalancerArn' --output text | sed 's|.*:loadbalancer/||')
TG_DIM=$(echo $ORDER_TG | sed 's|.*:||')

metric_alb() {
  aws cloudwatch get-metric-statistics \
    --namespace AWS/ApplicationELB --metric-name $1 \
    --dimensions Name=LoadBalancer,Value=$ALB_DIM Name=TargetGroup,Value=$TG_DIM \
    --start-time "$(date -u -v-1H +%Y-%m-%dT%H:%M:%S)" \
    --end-time   "$(date -u +%Y-%m-%dT%H:%M:%S)" \
    --period 300 --statistics $2 \
    --query "sort_by(Datapoints,&Timestamp)[].{T:Timestamp,V:$2}" --output text
}

echo "--- RequestCount";          metric_alb RequestCount Sum
echo "--- TargetResponseTime";    metric_alb TargetResponseTime Average
echo "--- HealthyHostCount";      metric_alb HealthyHostCount Average
echo "--- UnHealthyHostCount";    metric_alb UnHealthyHostCount Average
echo "--- HTTPCode_Target_5XX";   metric_alb HTTPCode_Target_5XX_Count Sum
echo "--- HTTPCode_ELB_5XX";      metric_alb HTTPCode_ELB_5XX_Count Sum
```

> **`HTTPCode_Target_5XX_Count` vs `HTTPCode_ELB_5XX_Count` — phân biệt này là chìa khoá:**
> - `Target_5XX` = **app của bạn** trả 500. Lỗi trong code Go.
> - `ELB_5XX` = **ALB tự sinh** 502/503/504. App không trả lời được:
>   không có target healthy (503), target đóng kết nối (502), timeout (504).
>
> Nhìn nhầm hai cái này là đi debug sai chỗ hoàn toàn.

**ECS (cần Container Insights, đã bật ở Bước 5.1):**

```bash
for m in CPUUtilization MemoryUtilization; do
  echo "--- $m"
  aws cloudwatch get-metric-statistics \
    --namespace AWS/ECS --metric-name $m \
    --dimensions Name=ClusterName,Value=$CLUSTER Name=ServiceName,Value=order-service \
    --start-time "$(date -u -v-1H +%Y-%m-%dT%H:%M:%S)" \
    --end-time   "$(date -u +%Y-%m-%dT%H:%M:%S)" \
    --period 300 --statistics Average \
    --query "sort_by(Datapoints,&Timestamp)[].{T:Timestamp,V:Average}" --output text
done

aws ecs describe-services --cluster $CLUSTER --services order-service product-service \
    --query 'services[].{Name:serviceName,Desired:desiredCount,Running:runningCount,Pending:pendingCount}' \
    --output table
```

### 13.3. Bảng chẩn đoán — lỗi nằm ở tầng nào

| Triệu chứng | Lỗi ở đâu | Kiểm tra bằng |
|---|---|---|
| `401 {"message":"Unauthorized"}` | API Gateway (authorizer) | `$context.authorizer.error` trong access log |
| `429 {"message":"Too Many Requests"}` | API Gateway (throttle) | `ThrottleCount` metric |
| `404 {"message":"Not Found"}` | API Gateway (không khớp route) | `get-routes` |
| `404 {"error":"no ALB rule matched"}` | ALB (không khớp rule) | `describe-rules` |
| `500 {"message":"Internal Server Error"}` | API Gateway ↔ backend | `$context.integrationErrorMessage` |
| `503 Service Unavailable` từ ALB | ALB — **không có target healthy** | `describe-target-health` |
| `502 Bad Gateway` từ ALB | Target chết giữa chừng / sai port | Log ECS + `HTTPCode_ELB_5XX` |
| `504 Gateway Timeout` | App xử lý quá lâu | `TargetResponseTime` + log ECS |
| `500` kèm JSON từ app | ECS (code Go) | `aws logs tail /ecs/$PROJECT/order-service` |

### 13.4. Truy vết một request xuyên 3 tầng

Mọi response của API Gateway đều có header `x-amzn-RequestId`:

```bash
REQ_ID=$(curl -s -D- -o /dev/null $API_URL/orders -H "Authorization: Bearer $(get_token)" \
  | grep -i 'x-amzn-requestid' | tr -d '\r' | awk '{print $2}')
echo "RequestId: $REQ_ID"

aws logs filter-log-events \
    --log-group-name /aws/apigateway/$PROJECT \
    --filter-pattern "\"$REQ_ID\"" \
    --query 'events[].message' --output text | jq
```

Từ log đó lấy `integrationLatency` và `integrationStatus` → biết ngay backend có trả lời không, chậm bao nhiêu.

### 13.5. Alarm tối thiểu nên có

```bash
aws cloudwatch put-metric-alarm \
    --alarm-name $PROJECT-apigw-5xx \
    --namespace AWS/ApiGateway --metric-name 5xx \
    --dimensions Name=ApiId,Value=$API_ID \
    --statistic Sum --period 60 --evaluation-periods 2 \
    --threshold 5 --comparison-operator GreaterThanThreshold \
    --treat-missing-data notBreaching

aws cloudwatch put-metric-alarm \
    --alarm-name $PROJECT-alb-unhealthy \
    --namespace AWS/ApplicationELB --metric-name UnHealthyHostCount \
    --dimensions Name=LoadBalancer,Value=$ALB_DIM Name=TargetGroup,Value=$TG_DIM \
    --statistic Average --period 60 --evaluation-periods 2 \
    --threshold 0 --comparison-operator GreaterThanThreshold \
    --treat-missing-data notBreaching
```

### ✅ Checkpoint Phase 12

```
API Gateway  → Count / 4xx / 5xx / Latency / IntegrationLatency / ThrottleCount
     ↓
ALB          → RequestCount / TargetResponseTime / HealthyHostCount / ELB_5XX vs Target_5XX
     ↓
ECS          → CPU / Memory / RunningCount + application log
```

Mỗi tầng có metric riêng, và **mỗi mã lỗi chỉ ra đúng một tầng.**

---

## 🔒 Bước 14 — Khoá ALB, chỉ cho API Gateway vào

Hiện tại ALB vẫn public — ai biết DNS là gọi thẳng, **bypass toàn bộ JWT và throttling**. Thử xem:

```bash
curl -s http://$ALB_DNS/orders | jq -r .instance   # 200, không cần token!
```

Có 2 cách xử lý.

### Cách A — Secret header (nhanh, đủ cho lab)

API Gateway thêm một header bí mật; ALB chỉ forward khi header đúng.

```bash
save SHARED_SECRET "$(openssl rand -hex 24)"

for INT in $INT_ORDERS $INT_ORDER_ID $INT_PRODUCTS $INT_PRODUCT_ID; do
  aws apigatewayv2 update-integration \
      --api-id $API_ID --integration-id $INT \
      --request-parameters "overwrite:header.X-Gateway-Secret=$SHARED_SECRET" >/dev/null
done
```

Sửa 2 rule để đòi header, và default action trả 403:

```bash
ORDER_RULE=$(aws elbv2 describe-rules --listener-arn $LISTENER_ARN \
  --query "Rules[?Priority=='10'].RuleArn" --output text)
PRODUCT_RULE=$(aws elbv2 describe-rules --listener-arn $LISTENER_ARN \
  --query "Rules[?Priority=='20'].RuleArn" --output text)

aws elbv2 modify-rule --rule-arn $ORDER_RULE --conditions \
  "Field=path-pattern,Values=[\"/orders\",\"/orders/*\"]" \
  "Field=http-header,HttpHeaderConfig={HttpHeaderName=X-Gateway-Secret,Values=[\"$SHARED_SECRET\"]}" \
  >/dev/null

aws elbv2 modify-rule --rule-arn $PRODUCT_RULE --conditions \
  "Field=path-pattern,Values=[\"/products\",\"/products/*\"]" \
  "Field=http-header,HttpHeaderConfig={HttpHeaderName=X-Gateway-Secret,Values=[\"$SHARED_SECRET\"]}" \
  >/dev/null
```

Test:

```bash
echo "--- gọi thẳng ALB (phải bị chặn)"
curl -s http://$ALB_DNS/orders | jq

echo "--- gọi qua API Gateway (phải OK)"
curl -s $API_URL/orders -H "Authorization: Bearer $(get_token)" | jq -r .instance
```

> `overwrite:` chứ không phải `append:` — nếu dùng `append`, client tự gửi `X-Gateway-Secret: sai`
> sẽ làm header thành `sai,<secret>` và ALB không khớp nữa. Ngược lại cũng có thể bị lợi dụng.
>
> Cách này chỉ là phòng tuyến mỏng: ALB vẫn nghe public, secret đi qua HTTP plaintext.
> Chấp nhận được cho lab, **không dùng cho production nếu chưa có HTTPS.**

### Cách B — Internal ALB + VPC Link (chuẩn production)

```
API Gateway ──VPC Link──> Internal ALB (không có public IP) ──> ECS
```

Với cách này ALB **không có địa chỉ public**, bypass là bất khả thi về mặt mạng, không cần secret nào.

```bash
save VPCLINK_ID "$(aws apigatewayv2 create-vpc-link \
    --name $PROJECT-vpclink \
    --subnet-ids $(echo $SUBNETS | tr ',' ' ') \
    --security-group-ids $ALB_SG \
    --query VpcLinkId --output text)"

# chờ VpcLink chuyển sang AVAILABLE (mất 2-5 phút)
while [ "$(aws apigatewayv2 get-vpc-link --vpc-link-id $VPCLINK_ID --query VpcLinkStatus --output text)" != "AVAILABLE" ]; do
  echo "waiting vpc link..."; sleep 20
done
```

Integration khi đó dùng **ARN của listener**, không phải DNS:

```bash
aws apigatewayv2 create-integration \
    --api-id $API_ID \
    --integration-type HTTP_PROXY \
    --integration-method ANY \
    --integration-uri $LISTENER_ARN \
    --connection-type VPC_LINK \
    --connection-id $VPCLINK_ID \
    --payload-format-version 1.0
```

> ⚠️ Đổi sang cách B cần **tạo lại ALB với `--scheme internal`** (scheme không sửa được sau khi tạo),
> và với ALB internal thì `integration-uri` là **ARN của Listener**, không phải `http://dns/path`.
> Điều đó có nghĩa: không truyền được path per-route nữa, phải dùng
> `--request-parameters 'overwrite:path=/orders/{id}'` để rewrite path.
>
> Trong phạm vi bài học này **dùng Cách A**; ghi lại Cách B để biết production làm gì khác.

---

## 💰 Bước 15 — Dọn dẹp

> **Cực kỳ quan trọng.** ALB tính tiền **theo giờ kể cả không có traffic** (~$16-18/tháng),
> Fargate tính theo giây × số task (5 task ~$25-30/tháng), NAT Gateway nếu có (~$32/tháng).
> Để quên một tuần là mất vài chục USD.

Xoá theo đúng thứ tự phụ thuộc:

```bash
source ~/aws-fnb/env.sh

# 1. ECS services (phải scale về 0 và xoá trước khi đụng tới TG)
for svc in order-service product-service; do
  aws ecs update-service --cluster $CLUSTER --service $svc --desired-count 0 >/dev/null 2>&1
  aws ecs delete-service --cluster $CLUSTER --service $svc --force >/dev/null 2>&1
done
aws ecs wait services-inactive --cluster $CLUSTER --services order-service product-service 2>/dev/null

# 2. ECS cluster + task definitions
aws ecs delete-cluster --cluster $CLUSTER >/dev/null
for fam in $PROJECT-order $PROJECT-product; do
  aws ecs list-task-definitions --family-prefix $fam --query 'taskDefinitionArns[]' --output text \
    | xargs -n1 -I{} aws ecs deregister-task-definition --task-definition {} >/dev/null
done

# 3. ALB: listener → rules (tự xoá theo listener) → ALB → target groups
aws elbv2 delete-listener --listener-arn $LISTENER_ARN
aws elbv2 delete-load-balancer --load-balancer-arn $ALB_ARN
sleep 30
aws elbv2 delete-target-group --target-group-arn $ORDER_TG
aws elbv2 delete-target-group --target-group-arn $PRODUCT_TG

# 4. API Gateway (xoá API là xoá luôn route/integration/stage/authorizer)
aws apigatewayv2 delete-api --api-id $API_ID
[ -n "$VPCLINK_ID" ] && aws apigatewayv2 delete-vpc-link --vpc-link-id $VPCLINK_ID

# 5. Lambda
aws lambda delete-function --function-name $PROJECT-system-info

# 6. Cognito
aws cognito-idp delete-user-pool --user-pool-id $POOL_ID

# 7. CloudWatch: alarms + log groups
aws cloudwatch delete-alarms --alarm-names $PROJECT-apigw-5xx $PROJECT-alb-unhealthy
for lg in /ecs/$PROJECT/order-service /ecs/$PROJECT/product-service \
          /aws/apigateway/$PROJECT /aws/lambda/$PROJECT-system-info; do
  aws logs delete-log-group --log-group-name $lg 2>/dev/null
done

# 8. ECR (--force để xoá cả image bên trong)
aws ecr delete-repository --repository-name $PROJECT/order-service   --force >/dev/null
aws ecr delete-repository --repository-name $PROJECT/product-service --force >/dev/null

# 9. Security groups (ECS SG tham chiếu ALB SG → xoá ECS SG trước)
sleep 20
aws ec2 delete-security-group --group-id $ECS_SG
aws ec2 delete-security-group --group-id $ALB_SG

# 10. IAM roles
aws iam detach-role-policy --role-name ecsTaskExecutionRole-$PROJECT \
    --policy-arn arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy
aws iam delete-role --role-name ecsTaskExecutionRole-$PROJECT

aws iam detach-role-policy --role-name lambdaRole-$PROJECT \
    --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole
aws iam delete-role --role-name lambdaRole-$PROJECT
```

Verify không còn gì tính tiền:

```bash
echo "--- ALB"; aws elbv2 describe-load-balancers --query 'LoadBalancers[].LoadBalancerName' --output text
echo "--- ECS"; aws ecs list-clusters --query 'clusterArns' --output text
echo "--- Tasks"; aws ecs list-tasks --cluster $CLUSTER 2>/dev/null
echo "--- NAT"; aws ec2 describe-nat-gateways --filter Name=state,Values=available --query 'NatGateways[].NatGatewayId' --output text
```

> ⚠️ **Xoá security group hay fail** với `DependencyViolation` vì ENI của Fargate task chưa được
> AWS thu hồi hết. Đợi 2-3 phút rồi chạy lại bước 9. Nếu vẫn kẹt:
> `aws ec2 describe-network-interfaces --filters Name=group-id,Values=$ECS_SG` để xem ENI nào còn giữ.

---

## 🧯 Bảng lỗi thường gặp

| Lỗi | Nguyên nhân | Sửa |
|---|---|---|
| `exec /server: exec format error` | Build arm64 trên Mac M-series, Fargate chạy x86 | `docker buildx build --platform linux/amd64` (Bước 3.3) |
| `CannotPullContainerError: i/o timeout` | Task không có đường ra internet để tới ECR | `assignPublicIp=ENABLED` hoặc thêm NAT Gateway |
| `ResourceInitializationError: failed to validate logger args` | Log group chưa tồn tại | Tạo log group trước (Bước 4.2) |
| `ECS was unable to assume the configured role` | IAM chưa propagate | Đợi 15s rồi thử lại |
| Service không bao giờ `stable`, task bị kill liên tục | ALB đánh unhealthy trước khi app kịp start | Tăng `--health-check-grace-period-seconds` |
| Target mãi ở `unused` / không register | Target group sai `--target-type` | Fargate **phải** là `ip` |
| Target `unhealthy`, reason `Target.Timeout` | ECS SG không cho ALB SG vào | `authorize-security-group-ingress --source-group $ALB_SG` |
| `/orders/1` OK nhưng `/orders` ra 404 | Path pattern chỉ có `/orders/*` | Khai cả `["/orders","/orders/*"]` |
| API Gateway trả `500` khi gọi Lambda | Thiếu `lambda add-permission` | Bước 10.3 |
| API Gateway trả `500` với `Invalid mapping expression` | Tên path param ở route ≠ ở integration URI | Đồng bộ tên `{id}` |
| `401` dù token mới lấy | Dùng AccessToken trong khi Audience = Client ID | Dùng **IdToken** |
| `route-settings` set xong không có tác dụng | Sai route key | Phải là `"GET /orders"` đủ method + space |
| `503` từ ALB | Không còn target healthy nào | `describe-target-health` |
| `DependencyViolation` khi xoá SG | ENI của task chưa thu hồi | Đợi 2-3 phút |

---

## 🧠 Kiến thức phải nắm sau project

### "Nếu bỏ component này đi thì hệ thống mất cái gì?"

**Bỏ ALB:**
```
API Gateway ──> ECS #1  (chỉ 1 IP cố định, phải tự cập nhật khi task đổi)
```
Mất: load balancing giữa nhiều task, health check để rút task chết, service discovery động.
API Gateway HTTP_PROXY trỏ tới một URI tĩnh — nó **không biết** có bao nhiêu task và task nào còn sống.

**Bỏ API Gateway:**
```
Internet ──> ALB ──> ECS
```
Vẫn chạy. Mất: JWT authorizer (phải nhét auth vào code của **cả 2** service),
throttling ở edge, route tường minh theo method, khả năng trỏ một path sang Lambda,
và một entry point duy nhất để quản lý.

**Bỏ ECS:**
```
API Gateway ──> ALB ──> ???
```
Không còn nơi chạy container. ALB không tự xử lý request.

**Bỏ Lambda:**
Hệ thống vẫn chạy nguyên vẹn. Lambda chỉ là **một loại integration target khác** —
điều nó chứng minh là API Gateway tách bạch *"API là gì"* khỏi *"ai xử lý"*.

### API Gateway vs ALB — bảng chốt

| | API Gateway | ALB |
|---|---|---|
| Vai trò | API management / entry point | Load balancer L7 |
| Biết số instance backend? | Không | Có, và health check từng cái |
| Định tuyến theo | route key (`GET /orders/{id}`) | rule (path, host, header, query) |
| Auth | JWT authorizer, Lambda authorizer, IAM | Chỉ OIDC/Cognito với listener HTTPS |
| Throttling | Có sẵn (rate + burst) | Không |
| Backend được | Lambda, HTTP, ALB/NLB, AWS service | EC2, IP (ECS), Lambda |
| Tính tiền | theo số request | theo giờ + LCU |
| Chạy trong VPC? | Không (managed, ngoài VPC) | Có |

**Một câu:** ALB trả lời *"gửi request này tới instance nào?"*, API Gateway trả lời
*"request này có được phép tồn tại không, và ai nên xử lý nó?"*.

### Checklist tự kiểm

```
[ ] Vẽ lại được đường đi của GET /orders/1 qua đủ 5 lớp mà không nhìn tài liệu
[ ] Giải thích được vì sao target-type phải là ip với Fargate
[ ] Giải thích được khác nhau giữa HTTPCode_ELB_5XX và HTTPCode_Target_5XX
[ ] Nhìn một mã lỗi (401/404/429/500/502/503/504) và chỉ đúng tầng gây ra
[ ] Giải thích được Rate vs Burst bằng ví dụ số
[ ] Giải thích được vì sao SG rule dùng --source-group thay vì --cidr
[ ] Biết vì sao request bị 401 không bao giờ tới ECS, và vì sao điều đó quan trọng
[ ] Dựng lại toàn bộ hệ thống từ đầu chỉ bằng file này trong dưới 45 phút
```

### 🟡 Chưa cần học ngay

API Gateway WebSocket · REST API (v1) nâng cao · Cognito Hosted UI / OAuth flows ·
AWS WAF · CloudFront · Global Accelerator · NLB · GWLB · ECS EC2 launch type · EKS · Service Mesh
