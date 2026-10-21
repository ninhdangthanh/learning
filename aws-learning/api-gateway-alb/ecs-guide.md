# ECS Fargate — Health Check và thay thế Task

Bản rút gọn từ `guide.md`, chỉ giữ phần **ECS**: dựng service, health check, và cách ECS tự thay task hỏng / chạy task mới.
Bỏ hết API Gateway, Lambda, JWT, throttling, test load balancing.

```
Internet ──> ALB :80 ──> Target Group (health check /health) ──> ECS Task #1 #2 #3
                                                                   ↑
                                               ECS Service giữ luôn đủ N task healthy
```

## 🗺️ Lộ trình

| Bước | Học được gì |
|---|---|
| 0 | Biến môi trường, VPC, subnet |
| 1 | Đẩy image lên ECR |
| 2 | IAM role, log group, security group |
| 3 | Cluster, Task Definition, Service (chưa có ALB) |
| 4 | Service tự bù task khi task chết |
| 5 | ALB + Target Group + health check |
| 6 | Gắn service vào Target Group |
| 7 | ⭐ ALB health check fail → ECS thay task |
| 8 | ⭐ Container health check trong Task Definition |
| 9 | ⭐ Deploy revision mới (rolling update) và force new deployment |
| 🧠 | Ghi chú: health check vs `desiredCount`, ai gọi `/health`, Target Group vs ALB node, so sánh video-call |
| 10 | Dọn dẹp |

---

## 🛠️ Bước 0 — Chuẩn bị

### 0.1. Biến môi trường

Mọi ARN/ID đều được lưu vào `env.sh` để tab terminal mới vẫn dùng được.

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
```

> ⚠️ **Mỗi tab terminal mới phải `source ./env.sh` trước.** Biến rỗng thì shell không báo lỗi gì, nó cứ thay chuỗi rỗng vào lệnh,
> rồi AWS CLI báo một lỗi chẳng liên quan (`Invalid ARN`, `expected one argument`...).

Quy ước: lấy được giá trị mới thì dùng `save TEN gia_tri` thay cho `export`.

### 0.2. VPC và subnet mặc định

```bash
save VPC_ID "$(aws ec2 describe-vpcs --filters Name=isDefault,Values=true \
    --query 'Vpcs[0].VpcId' --output text)"

save SUBNETS "$(aws ec2 describe-subnets --filters Name=vpc-id,Values=$VPC_ID \
    --query 'Subnets[].SubnetId' --output text | tr '\t' ',')"
```

> ALB cần **ít nhất 2 subnet ở 2 AZ khác nhau**.

---

## 📤 Bước 1 — Đẩy image lên ECR

ECS không đọc được image trên máy bạn, nên phải đẩy lên registry (ECR) trước.

```bash
aws ecr create-repository --repository-name $PROJECT/order-service >/dev/null

save ECR_BASE "$ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com"
save ORDER_IMAGE "$ECR_BASE/$PROJECT/order-service:v1"

aws ecr get-login-password --region $AWS_REGION \
  | docker login --username AWS --password-stdin $ECR_BASE

docker buildx build --platform linux/amd64 -t $ORDER_IMAGE ./order-service --push
```

> 🚨 **Mac M1/M2/M3:** `docker build` thường sẽ ra image **arm64**, còn Fargate mặc định chạy **X86_64**.
> Khi đó task start rồi chết ngay với `exec /server: exec format error`. Bắt buộc dùng `--platform linux/amd64`.

> Image nhỏ thì ECS pull nhanh, task start nhanh hơn. Đó là lý do Dockerfile dùng multi-stage build: image còn ~15MB thay vì ~800MB.

---

## 🔐 Bước 2 — IAM role, Log group, Security Group

### 2.1. Task Execution Role

Đây là quyền của **ECS agent** (kéo image từ ECR, ghi log lên CloudWatch), không phải quyền của code trong container.

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

> Đợi ~15 giây cho IAM propagate, nếu không sẽ gặp `ECS was unable to assume the configured role`.

| Role | Ai dùng | Dùng để làm gì |
|---|---|---|
| **Execution role** (`executionRoleArn`) | ECS agent | Pull image, ghi log, đọc secret lúc start |
| **Task role** (`taskRoleArn`) | Code trong container | Gọi S3, DynamoDB... (lab này không cần) |

### 2.2. Log group

```bash
aws logs create-log-group --log-group-name /ecs/$PROJECT/order-service
aws logs put-retention-policy --log-group-name /ecs/$PROJECT/order-service --retention-in-days 7
```

> ⚠️ Log group phải **tạo tay**: `AmazonECSTaskExecutionRolePolicy` không có quyền `logs:CreateLogGroup`.
> Nếu thiếu, task fail lúc start với `ResourceInitializationError: failed to validate logger args`.

### 2.3. Security Group cho task

```bash
save ECS_SG "$(aws ec2 create-security-group \
    --group-name $PROJECT-ecs-sg \
    --description "ECS tasks" \
    --vpc-id $VPC_ID \
    --query GroupId --output text)"

save MY_IP "$(curl -s https://checkip.amazonaws.com)"

aws ec2 authorize-security-group-ingress --group-id $ECS_SG \
    --protocol tcp --port 8080 --cidr $MY_IP/32
```

---

## 🚀 Bước 3 — Cluster, Task Definition, Service

### 3.1. Cluster

```bash
aws ecs create-cluster --cluster-name $PROJECT-cluster \
    --settings name=containerInsights,value=enabled >/dev/null

save CLUSTER "$PROJECT-cluster"
```

### 3.2. Task Definition

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

Output: `arn:aws:ecs:...:task-definition/fnb-order:1`. Số `:1` ở cuối là **revision**.

> Heredoc `<<EOF` **không** có nháy để `$EXEC_ROLE_ARN`, `$ORDER_IMAGE` được thay bằng giá trị thật.

| Field | Ý nghĩa |
|---|---|
| `family` | Tên nhóm. Mỗi lần `register` cùng family thì revision tăng 1 (`:1`, `:2`...) |
| `networkMode: awsvpc` | Mỗi task có ENI và IP riêng. Fargate bắt buộc dùng mode này |
| `cpu` / `memory` | Tài nguyên của cả task (256 = 0.25 vCPU) |
| `essential: true` | Container này chết thì cả task bị dừng |

### 3.3. Service với 1 task

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
```

> `assignPublicIp=ENABLED` là **bắt buộc** khi task nằm ở public subnet và không có NAT Gateway.
> Thiếu nó, task không có đường ra ECR, treo ở `PENDING` rồi fail với `CannotPullContainerError: ... i/o timeout`.

### 3.4. Các lệnh quan sát (dùng suốt các bước sau)

Thêm vào `env.sh` để tab nào cũng gọi được:

```bash
cat >> env.sh <<'EOF'

show_tasks() {
  local arns
  arns=$(aws ecs list-tasks --cluster $CLUSTER --service-name $1 --query 'taskArns[]' --output text)
  [ -z "$arns" ] && { echo "(khong co task nao)"; return; }
  aws ecs describe-tasks --cluster $CLUSTER --tasks $arns \
    --query 'tasks[].{Id:taskArn,TaskDef:taskDefinitionArn,Status:lastStatus,Health:healthStatus,Started:startedAt}' \
    --output table
}

show_stopped() {
  local arns
  arns=$(aws ecs list-tasks --cluster $CLUSTER --service-name $1 --desired-status STOPPED --query 'taskArns[]' --output text)
  [ -z "$arns" ] && { echo "(chua co task nao bi stop)"; return; }
  aws ecs describe-tasks --cluster $CLUSTER --tasks $arns \
    --query 'tasks[].{Id:taskArn,StopCode:stopCode,Reason:stoppedReason,Stopped:stoppedAt}' \
    --output table
}

show_events() {
  aws ecs describe-services --cluster $CLUSTER --services $1 \
    --query 'services[0].events[:10].[createdAt,message]' --output table
}

show_deployments() {
  aws ecs describe-services --cluster $CLUSTER --services $1 \
    --query 'services[0].deployments[].{Status:status,TaskDef:taskDefinition,Desired:desiredCount,Running:runningCount,Pending:pendingCount,Rollout:rolloutState}' \
    --output table
}
EOF

source ./env.sh
show_tasks order-service
```

| Lệnh | Xem gì |
|---|---|
| `show_tasks` | Task đang chạy: revision nào, `lastStatus`, `healthStatus` |
| `show_stopped` | Task đã chết: **vì sao** chết (`stoppedReason`). ECS giữ task stopped khoảng 1 giờ |
| `show_events` | Nhật ký của service: start task nào, stop task nào, vì sao |
| `show_deployments` | Tiến độ deploy: `PRIMARY` (mới) và `ACTIVE` (cũ) |

Xem log container:

```bash
aws logs tail /ecs/$PROJECT/order-service --follow
```

### ✅ Khái niệm cốt lõi

```
ECS Cluster → Service → Task → Container
                ↑         ↑
         Task Definition (bản thiết kế, có revision)
```

* **Task Definition**: bản thiết kế (image, CPU/RAM, port, log). **Bất biến**: muốn sửa thì phải `register` revision mới.
* **Task**: một lần chạy thật từ bản thiết kế. Có IP riêng và vòng đời riêng (`PROVISIONING → PENDING → RUNNING → STOPPED`).
* **Service**: luôn giữ đủ `desiredCount` task. Task chết thì service tạo task mới.

Vòng đời task:

```
PROVISIONING ─> PENDING ─> ACTIVATING ─> RUNNING ─> DEACTIVATING ─> STOPPING ─> DEPROVISIONING ─> STOPPED
 (cấp ENI)     (pull image)            (đang chạy)  (rút khỏi TG)
```

---

## ♻️ Bước 4 — Service tự bù task (chưa cần health check)

Scale lên 3:

```bash
aws ecs update-service --cluster $CLUSTER --service order-service --desired-count 3 >/dev/null
aws ecs wait services-stable --cluster $CLUSTER --services order-service
show_tasks order-service
```

Giết tay 1 task:

```bash
TASK=$(aws ecs list-tasks --cluster $CLUSTER --service-name order-service --query 'taskArns[0]' --output text)
aws ecs stop-task --cluster $CLUSTER --task $TASK --reason "hoc ECS: kill tay" >/dev/null
```

Đợi ~30 giây rồi xem:

```bash
show_events order-service
show_tasks order-service
show_stopped order-service
```

Bạn sẽ thấy:

```
(service order-service) has started 1 tasks: (task xxxx).
```

→ Service phát hiện `runningCount (2) < desiredCount (3)` nên tự start task mới. Task mới có **ID mới và IP mới**.

> ⚠️ **Hạn chế:** service chỉ biết task **chết hẳn** (process exit). Nếu process vẫn sống nhưng app treo hoặc trả lỗi 503,
> service vẫn coi là `RUNNING` và không làm gì. Phải có **health check** mới phát hiện được trường hợp này (Bước 5 → 8).

---

## ⚖️ Bước 5 — ALB + Target Group + Health Check

### 5.1. Security Group cho ALB

```bash
save ALB_SG "$(aws ec2 create-security-group \
    --group-name $PROJECT-alb-sg \
    --description "ALB public" \
    --vpc-id $VPC_ID \
    --query GroupId --output text)"

aws ec2 authorize-security-group-ingress --group-id $ALB_SG \
    --protocol tcp --port 80 --cidr 0.0.0.0/0

aws ec2 authorize-security-group-ingress --group-id $ECS_SG \
    --protocol tcp --port 8080 --source-group $ALB_SG
```

> **Dùng `--source-group`, không dùng `--cidr`.** IP của task đổi liên tục nên không whitelist IP được.
> Cho phép theo SG nghĩa là cái gì mang SG của ALB cũng vào được, bất kể IP.
> Thiếu rule này thì target sẽ `unhealthy` với reason `Target.Timeout`.

### 5.2. ALB

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
```

### 5.3. Target Group — nơi cấu hình health check

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

aws elbv2 modify-target-group-attributes \
    --target-group-arn $ORDER_TG \
    --attributes Key=deregistration_delay.timeout_seconds,Value=15
```

| Tham số | Giá trị | Ý nghĩa |
|---|---|---|
| `--health-check-path` | `/health` | ALB gọi `GET /health` vào từng task |
| `--matcher` | `200` | Chỉ HTTP 200 mới tính là pass |
| `--health-check-interval-seconds` | `15` | Cứ 15s check 1 lần |
| `--health-check-timeout-seconds` | `5` | Quá 5s không trả lời thì tính là fail |
| `--unhealthy-threshold-count` | `2` | Fail 2 lần liên tiếp thì `unhealthy` (~30s) |
| `--healthy-threshold-count` | `2` | Pass 2 lần liên tiếp thì `healthy` |
| `deregistration_delay` | `15` | Thời gian ALB đợi request đang chạy xong trước khi rút target ra (mặc định 300s) |

> 🚨 **`--target-type ip` là bắt buộc với Fargate.** Mặc định là `instance` (dành cho EC2). Mỗi Fargate task là một ENI có IP riêng.

Endpoint `/health` trong `order-service/main.go`:

```go
func health(w http.ResponseWriter, r *http.Request) {
	if !healthy.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"instance": instanceID, "status": "unhealthy"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "status": "ok"})
}
```

`POST /admin/toggle-health` lật cờ `healthy`, dùng để giả lập một task "chết lâm sàng": process vẫn sống nhưng `/health` trả về 503.

### 5.4. Listener :80

```bash
save LISTENER_ARN "$(aws elbv2 create-listener \
    --load-balancer-arn $ALB_ARN \
    --protocol HTTP --port 80 \
    --default-actions Type=forward,TargetGroupArn=$ORDER_TG \
    --query 'Listeners[0].ListenerArn' --output text)"
```

```
ALB ──> Listener :80 ──> Target Group ──> Target (IP của task)
                              ↑
                        Health Check
```

---

## 🔗 Bước 6 — Gắn ECS Service vào Target Group

Service ở Bước 3 được tạo **không có** load balancer. Cách đơn giản nhất là xoá đi rồi tạo lại có `--load-balancers`:

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
    --deployment-configuration "minimumHealthyPercent=100,maximumPercent=200,deploymentCircuitBreaker={enable=true,rollback=true}" \
    >/dev/null

aws ecs wait services-stable --cluster $CLUSTER --services order-service
```

> Cách khác: với service dùng rolling update, `aws ecs update-service --load-balancers ...` cũng gắn được LB mà không cần xoá service.
> Trong lab này xoá rồi tạo lại để thấy rõ từng bước.

| Tham số | Ý nghĩa |
|---|---|
| `--load-balancers` | ECS **tự register IP** của task vào TG khi task start, và **tự deregister** khi task stop. Bạn không bao giờ phải động tay vào IP |
| `containerName` / `containerPort` | Phải khớp với `name` và `containerPort` trong Task Definition |
| `--health-check-grace-period-seconds 30` | Trong 30s đầu sau khi task start, ECS **bỏ qua** kết quả health check của ALB |
| `--deployment-configuration` | Cách thay task khi deploy (xem Bước 9) |

> ⚠️ **Thiếu grace period:** app khởi động chậm sẽ bị ALB đánh `unhealthy` → ECS kill task → task mới start → lại bị kill.
> **Vòng lặp kill vô tận**, service không bao giờ `stable`. Grace period phải lớn hơn thời gian app cần để sẵn sàng.

Xem target đã register chưa:

```bash
aws elbv2 describe-target-health --target-group-arn $ORDER_TG \
    --query 'TargetHealthDescriptions[].{IP:Target.Id,State:TargetHealth.State,Reason:TargetHealth.Reason}' \
    --output table
```

Trạng thái target trong TG:

| State | Ý nghĩa |
|---|---|
| `initial` | Vừa register, đang chờ đủ số lần pass |
| `healthy` | Được nhận traffic |
| `unhealthy` | Fail health check, không nhận traffic |
| `draining` | Đang rút ra, chờ hết `deregistration_delay` |
| `unused` | Không có listener/rule nào trỏ tới TG, hoặc sai `--target-type` |

---

## ⭐ Bước 7 — ALB health check fail → ECS thay task

Đây là phần quan trọng nhất. Khi service đã gắn Target Group, **ECS dùng kết quả health check của ALB** để quyết định task còn sống hay không.

### 7.1. Luồng thay thế

```
App trả /health = 503
        │
        ▼
ALB fail 2 lần liên tiếp (2 × 15s ≈ 30s)
        │
        ▼
Target = unhealthy  ──>  ALB ngừng gửi traffic vào task này
        │
        ▼
ECS thấy task "failed ELB health checks"
        │
        ├──> Deregister task khỏi TG (draining, chờ deregistration_delay)
        ├──> Stop task (stoppedReason: Task failed ELB health checks in ...)
        └──> Start task MỚI để giữ đủ desiredCount
                 │
                 ▼
          Task mới: register vào TG → initial → healthy → nhận traffic
```

Có hai tầng làm hai việc khác nhau:

| Thành phần | Làm gì khi task hỏng |
|---|---|
| **ALB** | Ngừng gửi traffic vào task (bảo vệ request) |
| **ECS Service** | Giết task hỏng, tạo task mới thay thế (tự chữa lành) |

### 7.2. Thực hành

Làm 1 task bị bệnh (ALB đẩy request vào ngẫu nhiên 1 task):

```bash
curl -s -X POST http://$ALB_DNS/admin/toggle-health | jq
```

Ghi lại `instance` vừa bị hạ. Theo dõi target trong TG, cứ ~10s chạy lại một lần:

```bash
aws elbv2 describe-target-health --target-group-arn $ORDER_TG \
    --query 'TargetHealthDescriptions[].{IP:Target.Id,State:TargetHealth.State,Reason:TargetHealth.Reason}' \
    --output table
```

Thứ tự bạn sẽ thấy:

1. Một target chuyển sang `unhealthy`, reason `Target.ResponseCodeMismatch` (trả 503 thay vì 200)
2. Target đó chuyển sang `draining`
3. Một IP **mới** xuất hiện ở `initial`, rồi `healthy`

Xem ECS đã làm gì:

```bash
show_events order-service
```

```
(service order-service) has started 1 tasks: (task bbbb).
(service order-service) has stopped 1 running tasks: (task aaaa).
(service order-service) deregistered 1 targets in (target-group ...)
(service order-service) (task aaaa) failed ELB health checks in (target-group ...)
```

Xem vì sao task cũ bị stop:

```bash
show_stopped order-service
```

```
Reason: Task failed ELB health checks in (target-group arn:aws:elasticloadbalancing:...)
```

> 💡 Không cần gọi toggle lần nữa để bật lại: task bệnh đã bị **thay hẳn** bằng task mới. Cờ `healthy` sống trong memory của task cũ và chết theo task đó.

### 7.3. Đọc lỗi từ `stoppedReason` / `Reason`

| Thấy gì | Nghĩa là | Kiểm tra |
|---|---|---|
| `Target.ResponseCodeMismatch` | App trả sai HTTP code | Endpoint `/health`, `--matcher` |
| `Target.Timeout` | ALB không gọi tới được | SG của task có cho ALB SG vào port không |
| `Target.FailedHealthChecks` | Kết nối được nhưng fail | Log container |
| `Task failed ELB health checks` | ECS đã thay task vì ALB báo unhealthy | Nguyên nhân thật nằm ở `Reason` của TG |
| `Essential container in task exited` | Process chết | `aws logs tail` |
| `CannotPullContainerError` | Không pull được image | `assignPublicIp`, NAT, tên image |
| Task bị kill liên tục, service không `stable` | Grace period quá ngắn | Tăng `--health-check-grace-period-seconds` |

---

## ⭐ Bước 8 — Container health check (trong Task Definition)

ALB health check chỉ có khi service có load balancer. ECS còn có **container health check** riêng: chạy lệnh **ngay bên trong container**, không cần ALB.

### 8.1. So sánh

| | ALB health check | Container health check |
|---|---|---|
| Khai ở đâu | Target Group | Task Definition (`healthCheck`) |
| Ai thực hiện | ALB gọi qua network | ECS agent chạy lệnh trong container |
| Cần ALB không | Có | Không |
| Kiểm tra được | Đường mạng ALB → task + app | Chỉ app bên trong |
| Hiện ở đâu | `describe-target-health` | `healthStatus` của task (`show_tasks`) |
| Fail thì sao | ECS thay task (nếu service gắn TG) | ECS thay task (nếu container `essential`) |

Production thường bật **cả hai**.

### 8.2. Thêm `healthCheck` vào Task Definition

Image đã cài sẵn `curl` (Dockerfile có `apk add curl`), nên có thể dùng curl để check.

Sửa `order-taskdef.json`, thêm `healthCheck` vào container `order`:

```json
"healthCheck": {
  "command": ["CMD-SHELL", "curl -f http://localhost:8080/health || exit 1"],
  "interval": 10,
  "timeout": 5,
  "retries": 3,
  "startPeriod": 15
}
```

| Field | Ý nghĩa |
|---|---|
| `command` | Exit code `0` = pass, khác `0` = fail. `curl -f` trả lỗi khi gặp HTTP >= 400 |
| `interval` | Bao lâu chạy 1 lần (giây) |
| `timeout` | Lệnh chạy quá thời gian này thì tính là fail |
| `retries` | Fail bao nhiêu lần liên tiếp thì `UNHEALTHY` |
| `startPeriod` | Thời gian khởi động: fail trong khoảng này không bị tính |

Register revision mới:

```bash
aws ecs register-task-definition --cli-input-json file://order-taskdef.json \
    --query 'taskDefinition.taskDefinitionArn' --output text
```

→ Ra `fnb-order:2`. Revision `:1` vẫn còn nguyên, Task Definition là **bất biến**.

Deploy revision mới (chi tiết ở Bước 9):

```bash
aws ecs update-service --cluster $CLUSTER --service order-service \
    --task-definition $PROJECT-order >/dev/null
aws ecs wait services-stable --cluster $CLUSTER --services order-service
show_tasks order-service
```

> Truyền `--task-definition` chỉ với tên family (không có `:2`) thì ECS lấy **revision ACTIVE mới nhất**.

Cột `Health` giờ hiện `HEALTHY` thay vì `UNKNOWN`.

| `healthStatus` | Ý nghĩa |
|---|---|
| `UNKNOWN` | Không khai `healthCheck`, hoặc đang trong `startPeriod` |
| `HEALTHY` | Pass |
| `UNHEALTHY` | Fail đủ `retries` lần → ECS sẽ thay task |

Thử lại `toggle-health` như Bước 7.2 rồi xem `show_tasks`: task bị hạ sẽ chuyển `UNHEALTHY` rồi bị thay. Lần này có thể bị thay bởi cả hai cơ chế, cái nào phát hiện trước thì thay trước.

---

## ⭐ Bước 9 — Deploy revision mới và start lại task

### 9.1. Rolling update dùng health check như thế nào

Khi đổi Task Definition, ECS **không** giết hết task cũ rồi mới tạo task mới. Nó thay dần, và **chỉ rút task cũ khi task mới đã healthy**:

```
desiredCount = 3, minimumHealthyPercent = 100, maximumPercent = 200

  t0:  [v1][v1][v1]                         ← 3 task cũ
  t1:  [v1][v1][v1] [v2][v2][v2]            ← start thêm tối đa 200% = 6 task
  t2:  [v1][v1][v1] [v2✓][v2✓][v2✓]          ← chờ v2 healthy trong TG (+ container check)
  t3:  [v1↓][v1↓][v1↓] [v2][v2][v2]          ← v1 draining (deregistration_delay)
  t4:               [v2][v2][v2]            ← xong, không downtime
```

| Tham số | Ý nghĩa |
|---|---|
| `minimumHealthyPercent=100` | Lúc nào cũng phải còn ≥ 100% × desired task healthy → không được giết task cũ trước |
| `maximumPercent=200` | Được chạy tối đa 200% × desired task cùng lúc → đủ chỗ start task mới |
| `deploymentCircuitBreaker={enable=true,rollback=true}` | Task mới fail liên tục (không healthy được) thì ECS **dừng deploy và quay về revision cũ** |

> Ít tiền hơn, chấp nhận giảm capacity: `minimumHealthyPercent=50,maximumPercent=100` thì ECS giết bớt task cũ trước rồi mới tạo task mới.

### 9.2. Deploy revision mới

Ví dụ đổi biến môi trường. Sửa `order-taskdef.json`:

```json
"environment": [
  { "name": "PORT", "value": "8080" },
  { "name": "APP_VERSION", "value": "v2" }
]
```

```bash
aws ecs register-task-definition --cli-input-json file://order-taskdef.json \
    --query 'taskDefinition.taskDefinitionArn' --output text

aws ecs update-service --cluster $CLUSTER --service order-service \
    --task-definition $PROJECT-order >/dev/null
```

Theo dõi trong lúc deploy:

```bash
show_deployments order-service
```

```
| Status  | TaskDef      | Desired | Running | Pending | Rollout     |
| PRIMARY | fnb-order:3  | 3       | 1       | 2       | IN_PROGRESS |
| ACTIVE  | fnb-order:2  | 3       | 3       | 0       | COMPLETED   |
```

* `PRIMARY` = deployment mới, `ACTIVE` = deployment cũ đang bị rút dần.
* Xong khi chỉ còn 1 dòng `PRIMARY` với `Rollout = COMPLETED`.

```bash
aws ecs wait services-stable --cluster $CLUSTER --services order-service
show_tasks order-service
```

Cột `TaskDef` của tất cả task giờ là `:3`.

### 9.3. Đổi image (push code mới)

Nên dùng **tag mới** cho mỗi lần build:

```bash
save ORDER_IMAGE "$ECR_BASE/$PROJECT/order-service:v2"
docker buildx build --platform linux/amd64 -t $ORDER_IMAGE ./order-service --push
```

Rồi sửa `"image"` trong `order-taskdef.json` thành tag mới (hoặc chạy lại heredoc ở Bước 3.2), `register-task-definition`, rồi `update-service` như 9.2.

### 9.4. `--force-new-deployment` — start lại toàn bộ task, giữ nguyên revision

Dùng khi:
* Push image mới nhưng **giữ nguyên tag** (ví dụ `:latest`, `:v1`). Revision không đổi nên `update-service` thường không làm gì cả.
* Muốn restart mọi task (ví dụ để đọc lại secret hoặc config từ bên ngoài).

```bash
aws ecs update-service --cluster $CLUSTER --service order-service \
    --force-new-deployment >/dev/null

show_deployments order-service
```

ECS chạy rolling update giống 9.1, chỉ là revision cũ và mới là một. Task mới sẽ **pull lại image** theo tag nên lấy được bản vừa push.

### 9.5. Quay về revision cũ (rollback tay)

```bash
aws ecs update-service --cluster $CLUSTER --service order-service \
    --task-definition $PROJECT-order:2 >/dev/null
```

Liệt kê các revision đang có:

```bash
aws ecs list-task-definitions --family-prefix $PROJECT-order --query 'taskDefinitionArns[]' --output table
```

### 9.6. Khi deploy hỏng — circuit breaker

Nếu revision mới không bao giờ healthy (ví dụ `/health` sai, image lỗi), circuit breaker sẽ:

1. Đếm số task mới fail liên tiếp
2. Đánh dấu deployment `FAILED`
3. Tự `update-service` về revision cũ (do `rollback=true`)

```bash
show_deployments order-service
show_events order-service
```

```
Rollout: FAILED
(service order-service) (deployment ecs-svc/...) deployment failed: tasks failed to start.
(service order-service) rolling back to deployment ecs-svc/...
```

Trong suốt quá trình này, task cũ vẫn chạy và vẫn nhận traffic. Không có downtime.

### ✅ Tổng kết: các cách ECS start task mới

| Tình huống | Ai kích hoạt | Lệnh / cơ chế |
|---|---|---|
| Task chết (process exit) | Service | Tự động: `runningCount < desiredCount` |
| ALB health check fail | Service + TG | Tự động: `Task failed ELB health checks` |
| Container health check fail | Service | Tự động: task `UNHEALTHY` |
| Scale | Bạn | `update-service --desired-count N` |
| Kill tay 1 task | Bạn | `stop-task` → service bù task mới |
| Đổi config hoặc image (revision mới) | Bạn | `register-task-definition` + `update-service --task-definition` |
| Restart tất cả, giữ revision | Bạn | `update-service --force-new-deployment` |
| Rollback | Bạn / circuit breaker | `update-service --task-definition family:N` |

---

## 🧠 Ghi chú — Hiểu sâu về health check

### Health check khác gì `desiredCount`?

Hai cơ chế khác nhau, nhưng **làm việc cùng nhau**:

* **`desiredCount`** là con số mục tiêu: luôn có N task. ECS service liên tục so `runningCount` với `desiredCount`, thiếu thì start thêm.
  Nó chỉ biết **đếm**: process còn sống thì task vẫn được tính, dù app bên trong có hỏng.
* **Health check** dùng để phát hiện task **sống nhưng bệnh**, rồi bảo ECS stop task đó.

```
Health check fail ──> ECS stop task bệnh ──> runningCount < desiredCount ──> ECS start task mới
   (phát hiện)           (loại bỏ)                (đếm thiếu)                    (bù lại)
```

→ Health check lo **phát hiện và loại bỏ**, `desiredCount` lo **bù lại**. Task mới luôn được tạo ra nhờ `desiredCount`.

| Tình huống | Chỉ có `desiredCount` | Có thêm health check |
|---|---|---|
| Process crash (exit) | ✅ Tự bù task | ✅ Tự bù task |
| `stop-task` bằng tay | ✅ Tự bù task | ✅ Tự bù task |
| App treo, trả 503, mất DB nhưng process vẫn sống | ❌ Vẫn coi là `RUNNING`, không làm gì | ✅ Stop task bệnh rồi bù task mới |

Con số `desiredCount` quyết định có **downtime** khi thay task hay không:

* `desiredCount = 3`: thay 1 task thì 2 task còn lại vẫn nhận traffic → user không thấy gì.
* `desiredCount = 1`: task duy nhất bị stop thì ALB không còn target healthy nào → trả `503` khoảng 1–2 phút, đến khi task mới healthy.

### Ai gọi `/health`? Có qua API Gateway không?

**Không qua API Gateway.** Có hai "người" gọi `/health`:

```
            Traffic của user                         Health check
            ────────────────                         ────────────
Client
  │
  ▼
API Gateway  (không health check gì cả)
  │
  ▼
ALB Listener :80 ──> Rule ──> Target Group      Target Group health checker (ALB node)
                                   │                     │
                                   ▼                     │ GET http://<IP task>:8080/health
                              ECS Task  <────────────────┘  mỗi 15s, gọi thẳng vào IP task
                                   ▲
                                   │ curl http://localhost:8080/health
                              ECS agent (container health check, Bước 8)
```

| Ai gọi | Gọi vào đâu | Đi qua đường nào | Kết quả dùng để làm gì |
|---|---|---|---|
| **ALB node** (theo cấu hình Target Group) | `IP-task:8080/health` | Trực tiếp trong VPC. **Không** qua listener, rule hay API Gateway | ALB ngừng gửi traffic vào task bệnh, ECS thay task |
| **ECS agent** (nếu có `healthCheck` trong Task Definition) | `localhost:8080/health` trong container | Không đi ra network ngoài | ECS thay task khi task `UNHEALTHY` |

* **API Gateway không phải load balancer:** nó không biết có bao nhiêu task, không health check gì, chỉ forward request tới DNS của ALB.
* **Không có route `/health` trên API Gateway** → client gọi `/health` qua API Gateway sẽ nhận 404. Endpoint này là nội bộ, phục vụ hạ tầng.
* **Khi ALB không còn target healthy nào:** ALB trả `503`, API Gateway chuyển nguyên `503` đó về cho client.
* **ECS Service không tự gọi `/health`.** Nó chỉ **đọc kết quả** từ Target Group và ECS agent, rồi quyết định stop task nào và start task mới.

### Target Group chỉ là "cuốn sổ", sao lại check health được?

Target Group **không tự check**. Nó là cuốn sổ cấu hình, ghi:

1. **Danh sách target:** IP của các task, do ECS tự ghi vào và xoá ra.
2. **Quy tắc health check:** path, interval, matcher, threshold.
3. **Kết quả:** `healthy` / `unhealthy` của từng target. Cột này do ALB ghi vào.

Người **đi check thật** là các **node của ALB**. ALB không phải một máy duy nhất mà là một nhóm node, mỗi AZ trong `--subnets` có một node. Mỗi node đọc quy tắc trong sổ rồi tự gọi `/health` vào từng target.

```
            Target Group (cuốn sổ)
            ┌───────────────────────────────────────┐
            │ Quy tắc: GET /health, 15s, 200, x2    │
            │ Target:  10.0.1.23  → healthy         │◄── ALB node ghi kết quả
            │          10.0.2.87  → unhealthy       │
            │          10.0.3.11  → healthy         │
            └───────────────────────────────────────┘
                     ▲ đọc quy tắc        │
                     │                    ▼
            ALB node (AZ-a)   ALB node (AZ-b)      ← "người đi check"
                     │                    │
                     └── GET /health ─────┴──> từng IP task
```

Dấu hiệu cho thấy ALB mới là người check:

* **Target Group chưa gắn vào ALB** (chưa có listener/rule trỏ tới) thì target ở trạng thái `unused`, tức là không ai check.
* **ECS SG phải cho ALB SG vào** (`--source-group $ALB_SG`), vì request `/health` xuất phát từ IP của node ALB. Thiếu rule này thì target bị `Target.Timeout`.
* **Log app nhận `/health` nhiều hơn mức 15s/lần**, vì mỗi node ALB ở mỗi AZ check độc lập.

| Thành phần | Vai trò |
|---|---|
| **Target Group** | Cuốn sổ: ai là target, check theo quy tắc nào, kết quả ra sao |
| **ALB node** | Người đi check theo sổ, ghi kết quả, chỉ gửi traffic vào target `healthy` |
| **ECS Service** | Ghi IP task mới vào sổ, xoá IP task cũ ra, đọc kết quả để thay task bệnh |

Tách sổ khỏi người check có lợi: nhiều listener/rule có thể trỏ chung một Target Group, và ECS chỉ cần làm việc với Target Group mà không cần biết ALB có bao nhiêu node.

### Config nào làm ALB đi gọi health check? Nằm ở đâu?

Để ALB thật sự gọi `/health` vào task, cần **đủ 5 mảnh ghép**. Thiếu một mảnh là chuỗi bị đứt:

```
[1] Target Group: quy tắc check        (Bước 5.3)
        │
[2] Listener: nối ALB với Target Group  (Bước 5.4)  ← thiếu: target "unused", không ai check
        │
[3] ECS Service --load-balancers        (Bước 6)    ← thiếu: sổ không có IP nào để check
        │
[4] Security Group: ECS SG cho ALB SG    (Bước 5.1)  ← thiếu: Target.Timeout
        │
[5] App có endpoint /health trả 200     (main.go)   ← thiếu: Target.ResponseCodeMismatch
```

**[1] Target Group — quy tắc check** (Bước 5.3)

```bash
aws elbv2 create-target-group \
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
    --matcher HttpCode=200
```

| Tham số | Vai trò trong việc check |
|---|---|
| `--health-check-protocol` / `--health-check-path` | ALB node gọi `HTTP GET /health` |
| `--health-check-port` (không khai) | Mặc định `traffic-port` = port của target (8080, do ECS register) |
| `--health-check-interval-seconds` | Tần suất check |
| `--health-check-timeout-seconds` | Quá thời gian này là fail |
| `--healthy-threshold-count` / `--unhealthy-threshold-count` | Số lần liên tiếp để đổi trạng thái |
| `--matcher` | HTTP code nào được tính là pass |
| `--target-type ip` | Target là IP của task (Fargate bắt buộc) |

**[2] Listener — nối ALB với Target Group** (Bước 5.4)

```bash
aws elbv2 create-listener \
    --load-balancer-arn $ALB_ARN \
    --protocol HTTP --port 80 \
    --default-actions Type=forward,TargetGroupArn=$ORDER_TG
```

Đây là chỗ **ALB biết tới Target Group**. Chỉ khi có listener hoặc rule `forward` tới TG thì các node ALB mới đọc sổ và bắt đầu check.
Health check **không đi qua** listener này: nó chỉ là sợi dây nối, còn request `/health` đi thẳng từ node ALB vào IP task.

**[3] ECS Service — ghi IP task vào sổ** (Bước 6)

```bash
aws ecs create-service \
    ...
    --load-balancers "targetGroupArn=$ORDER_TG,containerName=order,containerPort=8080" \
    --health-check-grace-period-seconds 30
```

| Tham số | Vai trò |
|---|---|
| `--load-balancers` | ECS tự register `IP-task:containerPort` vào TG khi task start, deregister khi task stop. **Đồng thời** bật chế độ ECS đọc kết quả health check của TG để thay task |
| `--health-check-grace-period-seconds` | Trong N giây đầu, ECS **bỏ qua** kết quả `unhealthy` từ TG (ALB vẫn check, chỉ là ECS chưa phản ứng) |

**[4] Security Group — cho phép ALB node gọi vào** (Bước 5.1)

```bash
aws ec2 authorize-security-group-ingress --group-id $ECS_SG \
    --protocol tcp --port 8080 --source-group $ALB_SG
```

Request `/health` đi từ IP của node ALB (mang `$ALB_SG`) vào task (mang `$ECS_SG`).

**[5] App — endpoint `/health`** (`order-service/main.go`)

```go
mux.HandleFunc("GET /health", health)
```

Phải khớp với `--health-check-path` và trả HTTP code khớp `--matcher`.

**Xem và sửa config đang chạy:**

```bash
aws elbv2 describe-target-groups --target-group-arns $ORDER_TG \
    --query 'TargetGroups[0].{Path:HealthCheckPath,Port:HealthCheckPort,Interval:HealthCheckIntervalSeconds,Timeout:HealthCheckTimeoutSeconds,Healthy:HealthyThresholdCount,Unhealthy:UnhealthyThresholdCount,Matcher:Matcher.HttpCode,LB:LoadBalancerArns}' \
    --output table

aws elbv2 modify-target-group --target-group-arn $ORDER_TG \
    --health-check-path /health \
    --health-check-interval-seconds 10

aws ecs describe-services --cluster $CLUSTER --services order-service \
    --query 'services[0].{LB:loadBalancers,Grace:healthCheckGracePeriodSeconds}'

aws ecs update-service --cluster $CLUSTER --service order-service \
    --health-check-grace-period-seconds 60 >/dev/null
```

> Cột `LB` trong `describe-target-groups` rỗng nghĩa là TG chưa gắn vào ALB nào (thiếu mảnh [2]), nên không ai check cả.
> Sửa config health check của TG **có hiệu lực ngay**, không cần deploy lại ECS.

**Config container health check** (không liên quan ALB) nằm ở `healthCheck` trong Task Definition (Bước 8.2). Sửa nó thì phải `register` revision mới rồi `update-service`.

### So sánh với project video-call

`video-call/docs/AWS_DEPLOYMENT_GUIDE.md` dùng health check đơn giản hơn nhiều:

| | ecs-guide này | video-call |
|---|---|---|
| Endpoint check | `GET /health` riêng, trả 503 khi app bệnh | `GET /`, Nginx trả `index.html` tĩnh |
| Cấu hình Target Group | Tự chỉnh interval, threshold, deregistration | Mặc định của Console |
| Grace period | 30s | Không khai |
| Container health check | Có (Bước 8) | Không |
| Circuit breaker | Có | Không |
| `desiredCount` | 3 | 1 |

* **Điểm yếu:** health check `/` chỉ chứng minh **Nginx còn sống**, không nói gì về backend. Backend treo hoặc DB lỗi nhưng process vẫn sống thì `/` vẫn trả 200 → ECS không thay task.
* Backend **crash hẳn** thì task vẫn bị thay, vì backend có `essential: true`. Đây là nhờ `desiredCount`, không phải nhờ health check.
* **Lý do giữ đơn giản:** video-call giữ signaling hub trong memory và dùng SQLite trong container, nên chỉ chạy đúng với 1 task. Chạy 2 task song song (kể cả trong lúc rolling deploy) thì user có thể rơi vào 2 task khác nhau và không thấy nhau.
* **Muốn cải thiện thì:** thêm `GET /api/health` có ping DB, đổi health check path của Target Group sang `/api/health` (request đi qua Nginx vào backend nên check được cả hai), và thêm grace period 30–60s.

---

## 💰 Bước 10 — Dọn dẹp

> ALB tính tiền **theo giờ kể cả không có traffic**, Fargate tính theo giây × số task. Học xong nhớ xoá.

```bash
source ./env.sh

aws ecs update-service --cluster $CLUSTER --service order-service --desired-count 0 >/dev/null
aws ecs delete-service --cluster $CLUSTER --service order-service --force >/dev/null
aws ecs wait services-inactive --cluster $CLUSTER --services order-service

aws ecs delete-cluster --cluster $CLUSTER >/dev/null
aws ecs list-task-definitions --family-prefix $PROJECT-order --query 'taskDefinitionArns[]' --output text \
  | xargs -n1 -I{} aws ecs deregister-task-definition --task-definition {} >/dev/null

aws elbv2 delete-listener --listener-arn $LISTENER_ARN
aws elbv2 delete-load-balancer --load-balancer-arn $ALB_ARN
sleep 30
aws elbv2 delete-target-group --target-group-arn $ORDER_TG

aws logs delete-log-group --log-group-name /ecs/$PROJECT/order-service
aws ecr delete-repository --repository-name $PROJECT/order-service --force >/dev/null

sleep 20
aws ec2 delete-security-group --group-id $ECS_SG
aws ec2 delete-security-group --group-id $ALB_SG

aws iam detach-role-policy --role-name ecsTaskExecutionRole-$PROJECT \
    --policy-arn arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy
aws iam delete-role --role-name ecsTaskExecutionRole-$PROJECT
```

> Thứ tự quan trọng: service → cluster → ALB → TG → SG. ECS SG tham chiếu ALB SG nên phải xoá ECS SG trước.
> Xoá SG fail với `DependencyViolation` nghĩa là ENI của task chưa được thu hồi hết. Đợi 2-3 phút rồi chạy lại.

---

## 🧯 Bảng lỗi ECS thường gặp

| Lỗi | Nguyên nhân | Sửa |
|---|---|---|
| `exec /server: exec format error` | Image arm64, Fargate chạy x86 | `--platform linux/amd64` |
| `CannotPullContainerError: i/o timeout` | Task không ra được internet | `assignPublicIp=ENABLED` hoặc NAT |
| `ResourceInitializationError: failed to validate logger args` | Chưa có log group | Tạo log group trước |
| `ECS was unable to assume the configured role` | IAM chưa propagate | Đợi 15s |
| Task bị kill liên tục, service không `stable` | Grace period quá ngắn | Tăng `--health-check-grace-period-seconds` |
| Target mãi `unused` | Sai `--target-type` hoặc chưa có listener | Fargate **phải** là `ip` |
| Target `unhealthy` + `Target.Timeout` | ECS SG chặn ALB | `--source-group $ALB_SG` |
| `update-service` xong nhưng task không đổi | Revision không đổi (cùng tag image) | `--force-new-deployment` |
| Deployment `FAILED`, tự rollback | Task revision mới không healthy | `show_stopped` + `aws logs tail` |
| `503` từ ALB | Không còn target healthy nào | `describe-target-health` |
