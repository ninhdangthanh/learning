# Video Call Deploy — Review Cho Phỏng Vấn

File này đọc lại **đúng hạ tầng đang chạy** (lấy từ `.aws/task-definition.json`, `.github/workflows/video-call-deploy.yml` và Terraform export trong `aws-export/generated/aws/`), rồi biến nó thành câu chuyện kể khi phỏng vấn: kể gì, interviewer sẽ đào chỗ nào, và chỗ nào đang "demo-grade" cần nói trước là production sẽ làm khác.

Lý thuyết nền: [notes/aws-middle-notes.md](../../notes/aws-middle-notes.md).

---

## 1. Hạ tầng thực tế đang chạy

```text
Browser
  │ https://ninh-video-call-demo.food   (wss://…/ws cho signaling)
  ▼
Route 53 hosted zone ── A Alias ──► ALB videocall-alb (ap-southeast-1)
                                     ├─ :80  → redirect 301 sang HTTPS
                                     └─ :443 → ACM cert (domain + *.domain)
                                          ├─ rule: Host = ninh-video-call-demo.food → target group videocall-frontend-tg
                                          └─ default: 404 "Not Found" (chặn truy cập bằng DNS mặc định của ALB)
                                                  │ HTTP :80 (target type IP)
                                                  ▼
Default VPC 172.31.0.0/16, 3 public subnet (3 AZ), không NAT
  └─ ECS cluster videocall-cluster
       └─ Service videocall-service (Fargate, desiredCount=1, assign_public_ip=true)
            └─ Task videocall-task (0.5 vCPU / 1 GB, awsvpc)
                 ├─ frontend (nginx :80): serve React, proxy /api và /ws → localhost:8080
                 └─ backend  (Go :8080): REST + WebSocket signaling + SQLite /app/videocall.db

Media WebRTC: Browser ⇄ Browser (P2P qua STUN Google), TURN đọc từ SSM (chưa dựng coturn)

ECR: videocall-backend, videocall-frontend (tag = git SHA)
SSM Parameter Store: /videocall/JWT_SECRET, TURN_URLS, TURN_USERNAME, TURN_CREDENTIAL
CloudWatch Logs: /ecs/videocall (stream prefix backend/ frontend/)
IAM: ecsTaskExecutionRole (AmazonECSTaskExecutionRolePolicy + inline ssm:GetParameter(s) trên parameter/videocall/*)
     user github-actions-videocall-deploy (access key cho GitHub Actions)
CI/CD: push main (đổi trong video-call/**) → build 2 image → push ECR → render task def → deploy ECS, chờ service stable
ECS deploy: rolling min 100% / max 200%, deployment circuit breaker + rollback bật
```

Mỗi service được dùng và **lý do** (đây là phần interviewer muốn nghe):

| Service | Vai trò trong project | Vì sao chọn |
|---|---|---|
| ECR | Registry chứa image | Cùng IAM với ECS, pull nhanh trong region, không cần Docker Hub credential |
| ECS Fargate | Chạy container | Không quản server/patch OS; task definition gần giống docker-compose đã có sẵn |
| ALB | Cửa vào HTTPS/WSS | Layer 7, hỗ trợ WebSocket, terminate TLS bằng ACM, route theo host header, health check |
| ACM | Certificate | Miễn phí, tự gia hạn, validate bằng DNS CNAME trong Route 53 |
| Route 53 | DNS | Alias record trỏ thẳng root domain về ALB (CNAME không dùng được ở root) |
| SSM Parameter Store | JWT secret, TURN credential | Secret không nằm trong image/GitHub; ECS inject lúc start; standard tier free |
| CloudWatch Logs | Log 2 container | `awslogs` driver, debug khi task fail start |
| IAM | Execution role + user deploy | Execution role chỉ đọc đúng path `/videocall/*` (least privilege) |
| GitHub Actions | CI/CD | Repo ở GitHub, dùng action chính chủ của AWS |

**Vì sao HTTPS là bắt buộc chứ không phải "cho đẹp":** browser chỉ cho `getUserMedia` (camera/mic) trên secure context. Mở bằng `http://…elb.amazonaws.com` thì không gọi được camera → phải có domain + ACM + ALB 443.

**Vì sao media không đi qua ALB:** WebRTC media là UDP (DTLS-SRTP), ALB chỉ hiểu HTTP. ALB chỉ lo page, API và WebSocket signaling. Media đi P2P; khi NAT chặn P2P thì cần TURN server — TURN chạy UDP/TCP 3478 nên phải đặt trên **EC2 có Elastic IP** hoặc sau **NLB**, không đặt sau ALB.

---

## 2. Cách kể (pitch)

**30 giây:**

> Em làm app video call P2P bằng WebRTC, backend Go lo auth JWT và WebSocket signaling. Em tự deploy lên AWS: image build bằng GitHub Actions đẩy lên ECR, chạy trên ECS Fargate sau ALB có HTTPS từ ACM, domain quản lý bằng Route 53, secret để trong SSM Parameter Store, log về CloudWatch. Deploy là rolling update có circuit breaker tự rollback.

**2 phút** — thêm theo thứ tự:

1. Luồng request: domain → ALB 443 → nginx frontend → proxy `/api`, `/ws` sang backend cùng task qua `localhost` (cùng task Fargate thì chung network namespace).
2. Vì sao HTTPS bắt buộc (secure context) và vì sao media UDP không qua ALB.
3. Bảo mật: chỉ ALB public, listener rule theo Host header nên truy cập bằng DNS mặc định của ALB trả 404, backend 8080 không mở ra internet, secret qua SSM với role chỉ đọc đúng prefix.
4. **Chủ động nói giới hạn** (mục 3) và hướng production — đây là chỗ ghi điểm Middle.

---

## 3. Điểm yếu hiện tại — nói trước khi bị hỏi

Thành thật nói "đây là bản demo, nếu production em sẽ…" tốt hơn nhiều so với để interviewer tự phát hiện.

| # | Hiện trạng | Vấn đề | Production sẽ làm |
|---|---|---|---|
| 1 | SQLite nằm trong filesystem của task Fargate | Ephemeral storage: **mỗi lần deploy/task restart là mất toàn bộ user**. Không chạy được 2 task cùng ghi một file | RDS PostgreSQL (Multi-AZ) trong private subnet; tạm thời có thể mount EFS nhưng SQLite trên NFS vẫn không hợp cho nhiều writer |
| 2 | `desiredCount = 1`, frontend + backend chung 1 task | Single point of failure; không scale riêng được; deploy rolling vẫn ổn nhưng task chết là downtime đến khi task mới healthy | ≥ 2 task trên 2 AZ; tách frontend ra S3 + CloudFront, backend thành service riêng, ALB route `/api` `/ws` thẳng vào backend target group |
| 3 | WebSocket hub giữ state trong RAM | Scale lên 2 task: Alice ở task 1, Bob ở task 2 → không gửi được offer/answer cho nhau | Redis Pub/Sub (ElastiCache) để broadcast signaling giữa các task, hoặc API Gateway WebSocket API |
| 4 | Backend không có ping/pong, không có read deadline | ALB idle timeout mặc định **60s**: user mở app mà không gọi ai thì WS bị cắt; nginx đã đặt `proxy_read_timeout 3600s` nhưng ALB vẫn cắt ở 60s | Server gửi ping mỗi ~30s + `SetReadDeadline`/pong handler; hoặc tăng ALB idle timeout |
| 5 | Task ở public subnet, `assign_public_ip = true`, default VPC | Task có IP public (SG vẫn chặn, nhưng attack surface lớn hơn) | Private subnet + NAT Gateway, hoặc VPC endpoint cho ECR/SSM/CloudWatch Logs/S3 để khỏi cần NAT. Lý do demo chọn public: **NAT Gateway tốn tiền theo giờ** |
| 6 | ECS service gắn thêm **default SG** (`sg-0ab0…`) | Default SG cho phép mọi traffic từ chính nó; thừa và khó audit | Chỉ gắn `videocall-ecs-sg` (inbound 80 chỉ từ ALB SG) |
| 7 | GitHub Actions dùng IAM user + **access key lâu dài** | Lộ key = attacker deploy được; phải tự rotate | GitHub **OIDC** → assume role có trust policy giới hạn đúng repo/branch, quyền chỉ ECR push + `ecs:UpdateService`/`RegisterTaskDefinition` + `iam:PassRole` cho execution role |
| 8 | Health check target group là `/` của nginx | nginx trả 200 kể cả khi backend chết → ALB vẫn coi là healthy | Endpoint `/api/health` đi xuyên tới backend (ping DB); thêm container health check trong task definition |
| 9 | `health_check_grace_period = 0`, `deregistration_delay = 300` | Grace 0 có thể kill task mới trước khi app kịp start; drain 300s làm deploy chậm (WS dài vẫn bị cắt khi task cũ dừng) | Grace ~30–60s; drain phù hợp; client tự reconnect WebSocket |
| 10 | Container Insights tắt, không có alarm | Không biết khi task restart liên tục hay 5xx tăng | Bật Container Insights, alarm ALB 5xx / unhealthy host count → SNS → Slack |
| 11 | Account ID + ARN nằm trong `task-definition.json` trong repo | Không nguy hiểm trực tiếp nhưng lộ thông tin account | Template hóa (`${ACCOUNT_ID}`) hoặc quản bằng Terraform/CDK |
| 12 | Có 2 ACM cert trùng domain | Thừa, dễ nhầm khi đổi listener | Xóa cert không dùng |
| 13 | Không có TURN | User sau NAT đối xứng/mạng công ty không gọi được | coturn trên EC2 + Elastic IP (đã có [TURN_COTURN_AWS_SETUP.md](TURN_COTURN_AWS_SETUP.md)), hoặc dịch vụ TURN managed; credential TURN nên là loại tạm thời do backend ký (TURN REST API) thay vì username/password cố định gửi xuống client |
| 14 | Không có WAF / rate limit | Login/register có thể bị brute force | WAF rate-based rule trên ALB + rate limit theo user ở app (Redis) |

> Kiểm tra thêm: `video-call/name123.pem` nằm trong thư mục project và `.gitignore` không chặn `*.pem`. Nếu đó là private key EC2 thì không được commit — thêm `*.pem` vào `.gitignore`, và nếu đã từng push thì coi như key đã lộ, tạo key pair mới. `.tfstate` thì đã được ignore.

---

## 4. Kiến trúc "nếu làm production"

```text
Route 53 → CloudFront ─┬─ /*        → S3 (React build, Origin Access Control)
          (+ WAF)      └─ /api, /ws → ALB (public subnet, 2 AZ)
                                       └─ ECS Fargate backend service (private subnet, ≥2 task, auto scaling)
                                            ├─ RDS PostgreSQL Multi-AZ (private subnet)
                                            ├─ ElastiCache Redis: Pub/Sub signaling giữa task, rate limit, presence
                                            └─ Secrets Manager / SSM (qua VPC endpoint)
TURN: coturn trên EC2 + Elastic IP (hoặc NLB), mở UDP 3478 + dải relay port
CI/CD: GitHub OIDC → role deploy; image tag = SHA; rolling/blue-green + circuit breaker
Observability: CloudWatch Logs + Container Insights + alarms → SNS → Slack
```

Thứ tự ưu tiên nếu được hỏi "có 1 ngày thì sửa gì trước": (1) DB ra RDS — vì đang mất dữ liệu mỗi lần deploy; (2) WS ping/pong + health check thật; (3) OIDC thay access key; (4) tách service + Redis Pub/Sub để scale.

---

## 5. Câu hỏi interviewer có thể đào

**Sao frontend gọi backend bằng `localhost:8080`?**

> Hai container cùng một task Fargate dùng chung network namespace (`awsvpc`), nên nginx proxy sang `localhost:8080`. Nếu tách thành 2 service thì phải dùng ALB path routing hoặc Service Connect/Cloud Map để gọi nhau bằng DNS nội bộ.

**Task definition revision là gì? Deploy tạo ra cái gì?**

> Mỗi lần CI render image mới là register một revision mới (`videocall-task:8`, `:9`…). ECS service được update sang revision mới, chạy task mới, chờ healthy rồi dừng task cũ. Rollback là trỏ service về revision cũ.

**Project có mấy IAM role? Có phải 1 role cho CloudWatch lấy log, 1 role cho ECS pull image?**

> Không. Chỉ có **1 role** là `ecsTaskExecutionRole`. CloudWatch không tự "lấy" log; ECS agent đẩy log lên bằng chính execution role. Pull image, ghi log và đọc secret đều do ECS agent làm trước/khi container chạy, nên gom chung một role.

| Quyền | Đến từ | Dùng để |
|---|---|---|
| Pull image từ ECR | Managed policy `AmazonECSTaskExecutionRolePolicy` | Lấy image lúc start task |
| Ghi log CloudWatch | Cùng managed policy | `awslogs` driver đẩy log vào `/ecs/videocall` |
| `ssm:GetParameters` trên `parameter/videocall/*` | Inline policy tự thêm | Inject `JWT_SECRET`, TURN credential vào container |

> Managed policy **không** có `logs:CreateLogGroup` nên log group `/ecs/videocall` phải tạo tay trước. Ngoài role ra còn **IAM user** `github-actions-videocall-deploy` cho CI/CD — đó là user dùng access key, không phải role.

**Execution role khác task role? Project này có task role không?**

> Execution role cho ECS agent pull image từ ECR, ghi log, đọc SSM. Task role cho code gọi AWS lúc runtime. Backend hiện không gọi AWS API nên chưa cần task role; nếu thêm upload avatar lên S3 thì sẽ tạo task role có `s3:PutObject` trên đúng bucket/prefix.

**Đổi JWT_SECRET trong SSM thì app có nhận không?**

> Không, secret được inject lúc task start. Phải force new deployment. Đổi JWT secret cũng làm toàn bộ token cũ mất hiệu lực.

**ECS deploy fail thì sao?**

> ECS deployment circuit breaker đang bật với rollback: task mới không lên healthy sau số lần thử thì service tự quay về revision trước. GitHub Action đặt `wait-for-service-stability: true` nên job CI cũng fail theo, nhìn được ngay.

**Debug "task không start"?**

> Xem ECS → stopped task → stopped reason (thường là pull image lỗi, thiếu quyền đọc SSM, sai ARN parameter, hết RAM). Sau đó xem CloudWatch Logs stream `backend/…`, `frontend/…`. Nếu task start nhưng ALB báo unhealthy thì check SG (ALB SG có được vào port 80 không), health check path và port.

**Sao không dùng API Gateway?**

> API Gateway WebSocket API là mô hình khác (connection id, mỗi message là một lần gọi integration), phải viết lại hub. App đã có server WebSocket chạy liên tục trên container nên ALB hợp hơn. API Gateway hợp khi backend là Lambda hoặc cần API key/usage plan cho bên thứ 3.

**Chi phí tháng khoảng bao nhiêu, thứ gì tốn nhất?**

> Thứ chạy 24/7: ALB (tính theo giờ + LCU) và task Fargate 0.5 vCPU/1 GB. Route 53 hosted zone vài chục cent. Cố tình không dùng NAT Gateway vì riêng NAT đã tốn ngang hoặc hơn task. Không dùng thì scale service về 0 nhưng ALB vẫn tính tiền.
