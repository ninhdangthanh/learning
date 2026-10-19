# Video Call — Mô Tả Quy Trình Deploy Lên AWS (Cho Interviewer)

File này chỉ dùng để **kể lại cách deploy**: kiến trúc tổng quan, làm theo thứ tự nào, mỗi bước để làm gì, và lúc chạy CI/CD thì chuyện gì xảy ra. Không nói về WebRTC.

Phân tích điểm yếu và hướng production: [interview_deploy_review.md](interview_deploy_review.md).

---

## 1. Một câu tóm tắt

> App gồm 2 container (frontend nginx + backend Go). Em đẩy image lên ECR, chạy cả hai trong một task ECS Fargate, đặt sau Application Load Balancer có HTTPS từ ACM, domain trỏ về ALB bằng Route 53. Secret để trong SSM Parameter Store, log đẩy về CloudWatch. Mỗi lần push lên `main`, GitHub Actions tự build và rolling update ECS.

---

## 2. Kiến trúc tổng quan

```text
User
  │  https://ninh-video-call-demo.food
  ▼
Route 53 (A Alias record)
  ▼
Application Load Balancer — region ap-southeast-1
  ├─ :80  → redirect 301 sang HTTPS
  └─ :443 → TLS bằng ACM certificate → forward HTTP :80
  ▼
ECS Fargate service (videocall-service, cluster videocall-cluster)
  └─ Task videocall-task (0.5 vCPU / 1 GB)
       ├─ frontend  nginx :80   → serve React, proxy /api và /ws sang localhost:8080
       └─ backend   Go    :8080 → REST API + WebSocket

Xung quanh: ECR (image), SSM Parameter Store (secret), CloudWatch Logs (log), IAM (quyền), GitHub Actions (CI/CD)
```

Chỉ ALB được public ra internet. Backend `8080` không mở ra ngoài, request tới được backend chỉ qua nginx trong cùng task.

---

## 3. Các bước dựng hạ tầng (làm một lần)

Thứ tự này đi theo phụ thuộc: cái nào cần có trước thì tạo trước.

| Bước | Làm gì | Để làm gì |
|---|---|---|
| 1 | Chọn region `ap-southeast-1`, thống nhất tên resource | Gần user Việt Nam; tránh tình trạng ECR một region, ECS một region |
| 2 | Tạo 2 **ECR repository**: `videocall-backend`, `videocall-frontend` | Nơi chứa Docker image để ECS pull về chạy |
| 3 | Tạo **CloudWatch log group** `/ecs/videocall` | Container ghi log về đây; task lỗi thì xem ở đây |
| 4 | Tạo **SSM Parameter** dạng `SecureString`: `/videocall/JWT_SECRET`… | Secret không nằm trong code, image hay GitHub |
| 5 | Tạo **IAM role `ecsTaskExecutionRole`**: policy `AmazonECSTaskExecutionRolePolicy` + inline policy chỉ cho đọc `parameter/videocall/*` | Cho ECS pull image ECR, ghi log, đọc secret — đúng phạm vi cần, không hơn |
| 6 | Viết **task definition** (`.aws/task-definition.json`): 2 container, port, env, `secrets` trỏ ARN SSM, `awslogs`, Fargate, `awsvpc` | "Bản thiết kế" của task, tương tự docker-compose |
| 7 | Tạo **ECS cluster** `videocall-cluster` | Nhóm logic để chạy service |
| 8 | Xin **ACM certificate** cho domain + `*.domain`, validate bằng **DNS CNAME** trong Route 53 | Có HTTPS hợp lệ, AWS tự gia hạn. Cert phải cùng region với ALB |
| 9 | Tạo 2 **Security Group**: ALB SG (vào 80/443 từ internet), ECS SG (vào 80 **chỉ từ ALB SG**) | Internet chỉ chạm được ALB, không vào thẳng task |
| 10 | Tạo **target group** `videocall-frontend-tg`, target type **IP**, health check `/` | Fargate dùng `awsvpc`, mỗi task có IP riêng nên target phải là IP |
| 11 | Tạo **ALB** trên các public subnet nhiều AZ; listener 80 redirect sang 443; listener 443 gắn cert, rule `Host = domain` → target group, default trả 404 | Ép HTTPS; chỉ nhận request đúng domain, truy cập bằng DNS mặc định của ALB bị 404 |
| 12 | **Route 53**: tạo hosted zone, trỏ nameserver của domain về hosted zone, tạo **A Alias** record → ALB | User gõ domain là tới ALB. Alias dùng được cho root domain, CNAME thì không |
| 13 | Push image lần đầu, register task definition, tạo **ECS service** (Fargate, desired 1, gắn target group, bật deployment circuit breaker + rollback) | Service giữ task luôn chạy, tự đăng ký IP task vào target group, tự rollback nếu deploy lỗi. Chi tiết bootstrap: mục 5.3 |
| 14 | Tạo IAM user cho GitHub Actions, lưu key vào GitHub Secrets, thêm workflow | Bật CI/CD |

Kiểm tra sau khi dựng: mở `https://domain` thấy UI, gọi `/api/auth/login` được, WebSocket `wss://domain/ws` kết nối được, target group báo **healthy**.

---

## 4. Mỗi lần deploy (CI/CD)

File: `.github/workflows/video-call-deploy.yml`. Chạy khi push lên `main` có thay đổi trong `video-call/**`, hoặc bấm tay.

```text
push main
  → checkout code
  → configure AWS credentials
  → login ECR
  → docker build + push backend:<git-sha>
  → docker build + push frontend:<git-sha>
  → render task definition: thay image của 2 container bằng tag mới
  → register task definition revision mới (videocall-task:N+1) và update ECS service
  → chờ service stable (wait-for-service-stability)
```

Phía ECS khi nhận revision mới (rolling update, min 100% / max 200%):

1. Chạy task mới song song với task cũ.
2. Task mới đăng ký vào target group, ALB health check pass thì nhận traffic.
3. ALB drain task cũ (ngừng gửi request mới), sau đó ECS dừng task cũ.
4. Nếu task mới không lên healthy: **circuit breaker** tự rollback về revision cũ, job GitHub Actions cũng fail theo.

Ý cần nhấn mạnh:

* Image tag là **git SHA**, không dùng `latest` → biết chính xác commit nào đang chạy, rollback được bằng revision cũ.
* Deploy không downtime nhờ rolling update + health check.
* Secret được inject lúc task start, nên đổi secret trong SSM thì phải deploy lại.

---

## 5. Lần đầu bật CI/CD, và khi đổi task definition / pipeline

### 5.1. Task definition và revision

| Khái niệm | Ý nghĩa |
|---|---|
| **Family** | Tên task definition, ví dụ `videocall-task` |
| **Revision** | Phiên bản tăng dần: `videocall-task:1`, `:2`, `:3`… Đã tạo thì **không sửa được**, muốn đổi gì cũng phải tạo revision mới |

* Image tag là `github.sha`, nên **mỗi lần pipeline chạy sẽ có một revision mới**, và service được update sang revision đó.
* Revision mới sinh ra theo **mỗi lần pipeline chạy**, không phải theo mỗi lần có code mới: chạy lại cùng một commit vẫn sinh thêm revision.
* Revision cũ vẫn còn. Rollback nhanh mà không cần build lại: `aws ecs update-service --task-definition videocall-task:<số cũ>`.

### 5.2. Vì sao lần đầu phải tạo ECS service bằng tay

Bước cuối pipeline (`aws-actions/amazon-ecs-deploy-task-definition`) chỉ làm 2 việc theo thứ tự:

1. `RegisterTaskDefinition` → tạo revision mới
2. `UpdateService` → trỏ service **đã có sẵn** sang revision mới, ECS rolling update

Action này **không tạo service**. Nếu chưa có service, pipeline lỗi `ServiceNotFoundException` ở bước 2.

### 5.3. Cách bootstrap gọn nhất

```text
1. Dựng hạ tầng bước 1–12 (ECR, log group, SSM, IAM role, cluster, SG, TG, ALB, Route 53)
2. Chạy CI lần đầu → build + push image OK, register videocall-task:1 OK, fail ở UpdateService (chưa có service)
3. Tạo ECS service bằng tay, chọn revision CI vừa tạo (image tag = SHA, đã có trong ECR) → task chạy ngay
4. Từ lần push sau: CI chạy trọn vẹn (register :2 → update service → rolling update), không làm tay nữa
```

Ở bước 3, chọn task definition là chưa đủ. Phải cấu hình đúng các mục sau, nếu không task vẫn hỏng:

| Mục | Chọn gì |
|---|---|
| Launch type | Fargate |
| Network | VPC, subnet, **ECS SG** (chỉ cho ALB SG vào port 80) |
| Public IP | Task ở public subnet và không có NAT thì phải **bật**, nếu không sẽ không pull được image từ ECR |
| Load balancer | ALB có sẵn, container **`frontend:80`**, target group `videocall-frontend-tg` |
| Desired count | 1 |
| Deployment | Bật circuit breaker + rollback |

Nhớ chọn **đúng revision CI vừa tạo**, không chọn revision cũ có image tạm đã đăng ký tay (nếu có).

**Cách khác: tạo service trước khi có image trong ECR.** Cách này vẫn được, CI sẽ tự sửa:

* Task chết liên tục vì `CannotPullContainerError`. ECS thử lại, càng về sau càng giãn thời gian giữa các lần thử. Trong lúc đó ALB trả 503.
* Nếu bật circuit breaker, deployment đầu bị đánh dấu **FAILED**. Không có bản tốt nào để rollback, nhưng không sao.
* CI chạy → `UpdateService` tạo **deployment mới** với image có thật → service ổn định.
* Image ghi tạm trong revision đầu không quan trọng, vì CI sẽ thay nó bằng tag mới khi render.

### 5.4. Đổi task definition thì sao

**Đa số trường hợp không cần làm gì thêm.** Sửa `.aws/task-definition.json` rồi push. File này là "nguồn gốc": mỗi lần chạy, pipeline đọc nó, thay image, rồi đăng ký revision mới. Đổi CPU/memory, env, health check, tham số log… đều tự áp dụng. Không cần sửa pipeline, không cần chạy tay.

**Phải làm tay trước khi push** khi task definition trỏ tới thứ chưa tồn tại hoặc không khớp với service:

| Thay đổi | Cần làm tay trước |
|---|---|
| Thêm `secrets` trỏ tới SSM param mới | Tạo param, và cấp quyền đọc cho `ecsTaskExecutionRole` nếu param nằm ngoài `/videocall/*` |
| Đổi hoặc thêm `executionRoleArn` / `taskRoleArn` | Tạo role, cho IAM user của GitHub quyền `iam:PassRole` trên role đó |
| **Đổi tên container hoặc port** của container đang gắn với ALB (`frontend:80`) | `aws ecs update-service --load-balancers ...` để mapping của service khớp, nếu không thì deploy fail. Sửa luôn `CONTAINER_NAME_*` trong workflow |
| Thêm một container có image riêng | Tạo ECR repo mới, thêm bước build/push và render vào workflow |

**Những thứ thuộc service, không thuộc task definition**, thì pipeline không đổi được: desired count, subnet/SG, target group, autoscaling, circuit breaker. Muốn đổi thì dùng console, `aws ecs update-service` hoặc IaC (Terraform/CDK). Đây là lý do nhiều team quản lý service bằng IaC, còn CI/CD chỉ lo phần image.

### 5.5. Đổi pipeline (file workflow)

* Không cần chạy tay. Workflow có `on: push: branches: [main]`, push file workflow mới lên `main` là nó tự chạy bằng phiên bản mới.
* GitHub chỉ đọc workflow ở `.github/workflows/` **tại root của repo**. Repo root là `aws-learn/`, nên file `video-call/.github/workflows/deploy.yml` sẽ không chạy. File thật phải nằm ở `aws-learn/.github/workflows/video-call-deploy.yml` (có filter `paths: video-call/**`).

---

## 6. Request đi như thế nào sau khi deploy

```text
https://domain/            → Route 53 → ALB 443 (TLS terminate) → nginx → file React
https://domain/api/...     → Route 53 → ALB 443 → nginx /api → localhost:8080 (backend)
wss://domain/ws?token=...  → Route 53 → ALB 443 → nginx /ws (Upgrade) → localhost:8080/ws
```

Frontend gọi backend bằng `localhost` vì hai container cùng một task Fargate dùng chung network namespace.

---

## 7. Khi có lỗi thì debug ở đâu

| Triệu chứng | Xem ở đâu |
|---|---|
| Task không start | ECS → stopped task → **stopped reason** (pull image lỗi, thiếu quyền SSM, sai ARN) |
| App lỗi, 500 | CloudWatch Logs `/ecs/videocall`, stream `backend/…` |
| ALB trả 502/503 | Target group có healthy không; SG ECS có cho ALB SG vào port 80 không; health check path |
| Domain không vào được | Nameserver của domain đã trỏ về Route 53 chưa; A Alias đúng ALB chưa; cert ACM đã `Issued` chưa |
| CI fail | Log GitHub Actions: bước push ECR (quyền), hay bước chờ service stable (task không healthy) |

---

## 8. Câu kết khi kể

> Đây là bản demo nên em chạy 1 task, để task ở public subnet để khỏi tốn NAT Gateway, và DB vẫn là SQLite. Lên production em sẽ chuyển DB sang RDS, chạy ít nhất 2 task trên 2 AZ trong private subnet, tách frontend ra S3 + CloudFront, và dùng GitHub OIDC thay cho access key.
