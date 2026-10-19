# AWS Middle Backend Notes

Note này gom lý thuyết AWS ở mức **Middle Backend đi phỏng vấn**: mỗi service dùng để làm gì, khi nào chọn cái này thay vì cái kia, và trả lời thế nào cho đúng phạm vi của backend developer. Không đi sâu kiểu DevOps (không cần thuộc từng flag Terraform, không cần thiết kế landing zone nhiều account).

Nguyên tắc trả lời:

* Nói theo **vấn đề → service → vì sao chọn → trade-off**, không liệt kê tên service suông.
* Nói đúng phạm vi đã làm: "em viết Lambda + EventBridge, dựng ECS Fargate cho project cá nhân, còn VPC/IAM của công ty do DevOps quản". Interviewer đánh giá cao sự rõ ràng hơn là giả vờ biết hết.
* Luôn có một câu "nếu production thật thì em sẽ làm khác thế này…" — đó là chỗ thể hiện level Middle.

Project thực tế để kể: [video-call deploy review](../video-call/docs/interview_deploy_review.md) (ECS Fargate + ALB + Route 53 + ACM + ECR + SSM + CloudWatch + GitHub Actions), [aws-learning](../aws-learning/) (Lambda, SNS/SQS, S3 trigger, EventBridge), [multipartS3Upload](../multipartS3Upload/README.md) (S3 presigned multipart).

---

## 0. Bản đồ service theo nhóm

| Nhóm | Service cần biết | Một câu để nhớ |
|---|---|---|
| Nền tảng | Region/AZ, IAM, VPC, Security Group | Ai được làm gì (IAM), ai được nói chuyện với ai (VPC/SG) |
| Compute | EC2, ECS (Fargate/EC2), EKS, Lambda, App Runner | Chạy code ở đâu, ai quản server |
| Cửa vào | Route 53, ACM, ALB, NLB, API Gateway, CloudFront, WAF | Request từ internet đi vào hệ thống thế nào |
| Lưu trữ file | S3, EBS, EFS | Object vs block disk vs shared file system |
| Database | RDS, Aurora, DynamoDB, ElastiCache, DocumentDB, OpenSearch | SQL quản lý sẵn, NoSQL serverless, cache |
| Messaging | SQS, SNS, EventBridge, Kinesis, MSK, Amazon MQ | Queue, fan-out, event bus, stream |
| Config/secret | SSM Parameter Store, Secrets Manager, KMS | Không hard-code secret, mã hóa |
| Quan sát | CloudWatch (Logs/Metrics/Alarms), X-Ray, CloudTrail | Log app, metric, trace, audit ai đã làm gì |
| CI/CD | ECR, CodePipeline/CodeBuild/CodeDeploy, GitHub Actions + OIDC | Build image, deploy không downtime |

---

## 1. Region, Availability Zone, Edge

* **Region**: một vùng địa lý (vd `ap-southeast-1` Singapore). Data mặc định không tự rời region. Chọn region theo: gần user (latency), luật lưu trữ dữ liệu, giá, service có hỗ trợ không.
* **Availability Zone (AZ)**: một hoặc nhiều data center độc lập trong region (điện, mạng riêng). **High availability = chạy trên ít nhất 2 AZ.**
* **Edge location**: điểm của CloudFront/Route 53 gần user, dùng để cache và trả DNS nhanh.

Service global vs regional — câu hỏi bẫy hay gặp:

| Global | Regional |
|---|---|
| IAM, Route 53, CloudFront, WAF gắn CloudFront | EC2, ECS, Lambda, RDS, S3 bucket (tên global nhưng data nằm ở 1 region), SQS, ALB |

> Bẫy hay gặp: **ACM certificate cho CloudFront phải tạo ở `us-east-1`**, còn cert cho ALB phải cùng region với ALB.

---

## 2. IAM — ai được làm gì

| Khái niệm | Là gì | Dùng khi |
|---|---|---|
| User | Danh tính lâu dài, có password/access key | Người thật đăng nhập console (tốt nhất là qua SSO) |
| Group | Nhóm user để gán policy chung | Team dev, team ops |
| Role | Danh tính **tạm thời**, được "assume", không có key cố định | Service (EC2, ECS task, Lambda), CI/CD, cross-account |
| Policy | JSON: `Effect` + `Action` + `Resource` (+ `Condition`) | Gắn vào user/group/role/resource |

Cần trả lời được:

* **Least privilege**: chỉ cấp đúng action trên đúng resource. Ví dụ chỉ `ssm:GetParameters` trên `parameter/videocall/*`, không cấp `ssm:*` trên `*`.
* **Explicit Deny luôn thắng Allow.** Mặc định là deny.
* **Identity-based policy vs resource-based policy**: gắn vào role/user vs gắn vào resource (S3 bucket policy, SQS queue policy, Lambda resource policy — cho phép S3/EventBridge gọi Lambda).
* **Vì sao role tốt hơn access key**: credential tạm thời, tự rotate, không lộ trong code/GitHub.
* **ECS: Task Execution Role vs Task Role** — câu này gần như chắc chắn bị hỏi nếu ghi ECS trong CV:

| | Task Execution Role | Task Role |
|---|---|---|
| Ai dùng | ECS agent (hạ tầng) | Code của bạn trong container |
| Để làm gì | Pull image từ ECR, ghi log CloudWatch, đọc secret SSM/Secrets Manager để inject env | Gọi AWS API lúc runtime: đọc/ghi S3, gửi SQS, query DynamoDB |
| Tương đương ở Lambda | — | Lambda execution role |

* **GitHub Actions → AWS**: cách tốt là **OIDC** (GitHub xin token, assume role, không lưu access key). Cách cũ là lưu `AWS_ACCESS_KEY_ID` trong GitHub Secrets — chạy được nhưng là long-lived credential.

---

## 3. VPC — mạng riêng

```text
VPC 10.0.0.0/16 (một region)
├── AZ-a
│   ├── Public subnet   → route 0.0.0.0/0 → Internet Gateway   (ALB, NAT GW, bastion)
│   └── Private subnet  → route 0.0.0.0/0 → NAT Gateway        (ECS task, EC2 app)
│       └── DB subnet   → không route ra internet               (RDS, ElastiCache)
└── AZ-b  (lặp lại y hệt để HA)
```

| Khái niệm | Là gì |
|---|---|
| Subnet | Dải IP con, **nằm trong đúng 1 AZ** |
| Public subnet | Route table có route ra **Internet Gateway** |
| Private subnet | Không có route ra IGW; muốn ra internet (gọi API bên ngoài, pull image) phải qua **NAT Gateway** |
| Internet Gateway | Cổng 2 chiều giữa VPC và internet |
| NAT Gateway | Cho private subnet đi **ra** internet, internet không gọi **vào** được. Tốn tiền theo giờ + GB |
| VPC Endpoint | Gọi S3/DynamoDB (Gateway endpoint, free) hoặc ECR/SSM/SQS (Interface endpoint, PrivateLink) **không qua internet/NAT** |
| VPC Peering / Transit Gateway | Nối nhiều VPC với nhau |

**Security Group vs NACL** — câu kinh điển:

| | Security Group | Network ACL |
|---|---|---|
| Gắn vào | ENI (EC2, task, RDS, ALB) | Subnet |
| Stateful? | **Stateful**: cho request vào thì response tự được ra | **Stateless**: phải mở cả chiều về |
| Rule | Chỉ Allow | Allow và Deny, có thứ tự số |
| Tham chiếu | Được tham chiếu SG khác ("chỉ cho ALB SG vào port 80") | Chỉ CIDR |
| Dùng thực tế | Công cụ chính hằng ngày | Lớp chặn thô, ít khi đụng |

Pattern chuẩn: `Internet → ALB SG (443 từ 0.0.0.0/0) → App SG (8080 chỉ từ ALB SG) → DB SG (5432 chỉ từ App SG)`.

---

## 4. Compute — chạy code ở đâu

| | EC2 | ECS on EC2 | ECS Fargate | EKS | Lambda | App Runner / Beanstalk |
|---|---|---|---|---|---|---|
| Bạn quản | OS, patch, runtime, scale | Cluster EC2 + container | Chỉ container | K8s workload (+ node group) | Chỉ function code | Chỉ code/image |
| Đơn vị | Máy ảo | Task (1+ container) | Task | Pod | Function invocation | Service |
| Tính tiền | Theo giờ instance | Theo EC2 | Theo vCPU/RAM của task theo giây | Control plane + node | Theo request + thời gian chạy (ms) | Theo instance active |
| Hợp với | Cần kiểm soát OS, phần mềm đặc thù (coturn, GPU), app cũ | Tải lớn ổn định, cần tối ưu giá | Microservice container, team không có ops | Công ty đã chuẩn hóa K8s, nhiều team | Event-driven, cron, tải không đều, glue code | Demo, app nhỏ, muốn nhanh |
| Không hợp | Muốn ít vận hành | Team nhỏ | Cần privileged/GPU/host network | Team nhỏ, ít service | Job > 15 phút, kết nối dài (WebSocket server), cần latency ổn định tuyệt đối | Hệ thống phức tạp |

**Lambda — các điểm hay hỏi:**

* Timeout tối đa **15 phút**; RAM 128 MB → 10 GB (CPU tăng theo RAM).
* **Cold start**: lần đầu/scale thêm phải khởi tạo môi trường → chậm hơn. Giảm bằng: runtime nhẹ (Go, Node), package nhỏ, khởi tạo client DB/SDK **ngoài handler** để reuse, Provisioned Concurrency nếu cần.
* **Concurrency**: mỗi request đồng thời là một execution environment. Có giới hạn concurrency theo account/region; dùng **Reserved Concurrency** để không cho một function ăn hết hoặc để bảo vệ DB phía sau.
* **Lambda + RDS**: scale lên 1000 concurrency = 1000 connection → nổ DB. Giải pháp: **RDS Proxy**, hoặc giới hạn concurrency, hoặc dùng DynamoDB.
* **Invocation type**: sync (API Gateway gọi, chờ kết quả), async (S3/SNS/EventBridge, Lambda tự retry 2 lần, có DLQ/on-failure destination), poll-based (SQS/Kinesis — Lambda tự poll, batch).
* Stateless: không giữ state giữa các lần gọi (biến global có thể còn nhưng không được dựa vào).

**ECS — từ vựng cần nói trơn tru:**

| Từ | Nghĩa |
|---|---|
| Cluster | Nhóm logic chứa service/task |
| Task Definition | "Bản thiết kế" như docker-compose: image, CPU/RAM, port, env, secret, log, role. Có version (`videocall-task:8`) |
| Task | Một instance đang chạy của task definition |
| Service | Giữ đúng `desiredCount` task, thay task chết, gắn với ALB target group, rolling update |
| Launch type | Fargate (serverless) hoặc EC2 |
| `awsvpc` network mode | Mỗi task có ENI + IP riêng, gắn SG riêng — bắt buộc với Fargate |

**ECS vs EKS** trả lời ngắn: ECS đơn giản, gắn chặt AWS, ít khái niệm; EKS là Kubernetes chuẩn, portable, hệ sinh thái lớn (Helm, operator) nhưng phức tạp hơn và tốn chi phí control plane. Team nhỏ, chỉ chạy trên AWS → ECS Fargate.

---

## 5. Cửa vào: Route 53, ACM, ALB, NLB, API Gateway, CloudFront

```text
User → Route 53 (DNS) → CloudFront (CDN, optional) → WAF → ALB / API Gateway → ECS / Lambda / EC2
```

| Service | Làm gì |
|---|---|
| Route 53 | DNS: hosted zone, record A/AAAA/CNAME/**Alias** (trỏ thẳng tới ALB/CloudFront, dùng được ở root domain, free query), routing policy (weighted, latency, failover), health check |
| ACM | Cấp và **tự gia hạn** certificate TLS miễn phí cho ALB/CloudFront/API Gateway. Validate bằng DNS (CNAME) |
| ALB | Load balancer **Layer 7** (HTTP/HTTPS/WebSocket/gRPC): route theo path/host/header, terminate TLS, health check |
| NLB | **Layer 4** (TCP/UDP/TLS): cực nhanh, IP tĩnh, giữ nguyên source IP, cho protocol không phải HTTP |
| API Gateway | "API management": auth (JWT/Cognito/Lambda authorizer), API key, throttling/quota, request validation, tích hợp Lambda trực tiếp, có loại WebSocket API |
| CloudFront | CDN: cache static/S3 ở edge, giảm latency, chặn DDoS cơ bản (Shield Standard) |
| WAF | Firewall Layer 7: chặn SQLi/XSS, rate limit theo IP, geo block. Gắn vào CloudFront/ALB/API Gateway |

**ALB vs NLB vs API Gateway:**

| Tình huống | Chọn |
|---|---|
| Web app/REST/WebSocket chạy trên ECS/EC2, route theo path `/api`, `/ws` | **ALB** |
| TCP/UDP thuần (game, MQTT, TURN server), cần IP tĩnh, cần throughput cực cao | **NLB** |
| Backend là Lambda, cần API key/usage plan/throttle per client, public API cho bên thứ 3 | **API Gateway** |
| Static frontend (React build) | **S3 + CloudFront** |

Điểm hay hỏi:

* **ALB idle timeout mặc định 60s**: WebSocket/SSE không có traffic sẽ bị ALB cắt → server phải gửi ping/heartbeat định kỳ hoặc tăng idle timeout.
* **Sticky session**: ALB có cookie stickiness, nhưng đừng dựa vào nó để giữ state; ưu tiên app stateless + Redis.
* **Health check**: target group gọi `/health`; fail N lần → target bị loại; ECS service sẽ thay task.
* **Connection draining / deregistration delay**: khi deploy, ALB ngừng gửi request mới cho task cũ và chờ request đang chạy xong.
* API Gateway có **giới hạn timeout tích hợp** (mặc định ~29s) → job dài phải làm async (trả 202, xử lý qua SQS/Lambda).

---

## 6. Lưu trữ: S3 vs EBS vs EFS

| | S3 | EBS | EFS |
|---|---|---|---|
| Kiểu | Object storage (key → object), truy cập qua HTTP API | Block disk gắn vào **1 EC2** (trong 1 AZ) | Network file system (NFS), **nhiều máy mount cùng lúc**, multi-AZ |
| Dùng cho | File upload, ảnh, backup, log, data lake, static website | Ổ đĩa OS, database tự cài trên EC2 | File dùng chung giữa nhiều container/EC2, persistent volume cho ECS/Fargate |
| Scale | Gần như vô hạn | Cố định theo size đã cấp | Tự co giãn |

**S3 — các điểm phải nắm:**

* Bucket name global unique; object có key, không có "folder" thật (prefix).
* **Durability 11 số 9**; strong read-after-write consistency.
* **Storage class**: Standard → Standard-IA (ít truy cập) → Glacier (archive, lấy chậm) → Intelligent-Tiering (tự chuyển). Kết hợp **Lifecycle rule** để tự chuyển/xóa, và xóa multipart upload dang dở.
* **Presigned URL**: backend ký URL có thời hạn, client upload/download **trực tiếp** S3, backend không phải chịu băng thông. Ký bằng quyền của credential tạo ra nó.
* **Multipart upload**: file lớn chia part (part tối thiểu 5 MB, trừ part cuối), upload song song, retry từng part, cuối cùng `CompleteMultipartUpload`. Kết hợp presigned URL cho từng part — đúng cái đã làm ở DIQIT.
* **Block Public Access** bật mặc định; muốn public file thì dùng CloudFront + Origin Access Control thay vì mở bucket public.
* **Event notification**: S3 → Lambda/SQS/SNS/EventBridge khi có object mới (resize ảnh, scan virus, index).
* Versioning để chống xóa nhầm; encryption mặc định SSE-S3, cần kiểm soát key thì SSE-KMS.
* CORS phải cấu hình nếu browser upload thẳng lên S3 (và expose header `ETag` cho multipart).

---

## 7. Database

| Nhu cầu | Service | Ghi chú |
|---|---|---|
| SQL quen thuộc (Postgres/MySQL), quản lý sẵn backup/patch | **RDS** | Multi-AZ, read replica, automated backup |
| SQL cần hiệu năng/HA cao hơn | **Aurora** | Storage tự nhân 6 bản trên 3 AZ, failover nhanh, tới 15 read replica, có Serverless v2 |
| Key-value/document, scale khổng lồ, latency ms ổn định, serverless | **DynamoDB** | Thiết kế theo access pattern (partition key/sort key), không join |
| Cache, session, rate limit, leaderboard, pub/sub | **ElastiCache** (Redis/Valkey, Memcached) | Đúng những gì CV đã làm với Redis |
| MongoDB-compatible quản lý sẵn | **DocumentDB** (hoặc MongoDB Atlas trên AWS) | Không phải MongoDB thật 100%, một số tính năng khác |
| Full-text search, log analytics | **OpenSearch** | Fork của Elasticsearch |

**Multi-AZ vs Read Replica** — câu chắc chắn gặp:

| | Multi-AZ | Read Replica |
|---|---|---|
| Mục đích | **High availability** (chống sập) | **Scale đọc** |
| Replication | Synchronous sang standby ở AZ khác | Asynchronous (có replication lag) |
| Standby có nhận traffic? | Không (bản cổ điển) | Có, cho query đọc |
| Khi primary chết | Tự failover, DNS endpoint giữ nguyên | Phải promote thủ công (trừ Aurora) |

Trả lời "khi nào DynamoDB, khi nào RDS": dữ liệu quan hệ, transaction nhiều bảng, query ad-hoc, report → RDS/Aurora. Access pattern biết trước, cần scale rất lớn, key-value (session, cart, event, IoT) → DynamoDB.

---

## 8. Messaging: SQS vs SNS vs EventBridge vs Kinesis vs MSK vs Amazon MQ

| Service | Mô hình | Dùng khi |
|---|---|---|
| **SQS** | Queue, consumer **pull**, mỗi message 1 consumer xử lý | Tách producer/consumer, buffer tải đột biến, background job |
| **SNS** | Pub/sub **push**, 1 message → nhiều subscriber (SQS, Lambda, HTTP, email/SMS) | Fan-out, notification |
| **EventBridge** | Event bus + rule lọc theo nội dung event, **Scheduler** (cron) | Event-driven giữa service, nhận event AWS/SaaS, cron job |
| **Kinesis Data Streams** | Stream có thứ tự theo shard, replay được, nhiều consumer đọc cùng data | Clickstream, log, analytics real-time |
| **MSK** | Kafka quản lý sẵn | Đã dùng Kafka, cần ecosystem Kafka |
| **Amazon MQ** | RabbitMQ/ActiveMQ quản lý sẵn | Migrate app đang dùng RabbitMQ/AMQP mà không sửa code |

**SQS cần nắm:**

* **Standard**: throughput gần như không giới hạn, **at-least-once** (có thể trùng), thứ tự best-effort.
* **FIFO**: đúng thứ tự trong **message group**, exactly-once processing trong khoảng dedup, throughput thấp hơn.
* **Visibility timeout**: consumer nhận message → message bị ẩn trong X giây; xử lý xong phải **delete**, không thì hết timeout message hiện lại → có thể xử lý lặp. Đặt timeout > thời gian xử lý.
* **DLQ**: nhận quá `maxReceiveCount` lần mà vẫn fail → chuyển sang DLQ để điều tra.
* **Long polling** (`WaitTimeSeconds` tới 20s) giảm request rỗng, giảm tiền.
* Message có giới hạn kích thước nhỏ → payload lớn thì lưu S3, gửi key qua SQS.
* Vì at-least-once nên consumer phải **idempotent** — map thẳng về idempotent consumer + dedup key đã làm ở DIQIT.

**SNS + SQS fan-out**: 1 event `order.created` → SNS topic → nhiều SQS queue (inventory, email, analytics), mỗi service có queue riêng, chết thì message vẫn nằm chờ. Lab: [aws-learning/sns-sqs](../aws-learning/sns-sqs).

**So với RabbitMQ (CV):**

| RabbitMQ | AWS tương đương |
|---|---|
| Queue + worker | SQS |
| Fanout/topic exchange | SNS (+ filter policy) hoặc EventBridge rule |
| DLX + retry queue với TTL | SQS DLQ + redrive, delay queue/visibility timeout |
| Phải tự vận hành cluster | SQS/SNS serverless, không quản broker |

> Câu trả lời mẫu "sao không dùng SQS thay RabbitMQ?": hệ thống đã chạy RabbitMQ với routing phức tạp (exchange, routing key), team đã quen, và chạy được cả ngoài AWS. Nếu làm mới hoàn toàn trên AWS và chỉ cần queue + fan-out thì SQS/SNS ít vận hành hơn.

**EventBridge vs SNS**: SNS đơn giản, nhanh, fan-out số lượng lớn. EventBridge lọc theo nội dung event linh hoạt, có schema registry, archive/replay, nhận event từ AWS service và SaaS, có Scheduler. Cron hằng ngày → EventBridge Scheduler → Lambda (đúng với CV).

---

## 9. Config, secret, mã hóa

| | SSM Parameter Store | Secrets Manager |
|---|---|---|
| Dùng cho | Config + secret đơn giản (`SecureString`) | Secret cần **tự rotate** (password RDS) |
| Giá | Standard tier miễn phí | Tính tiền theo secret/tháng + API call |
| Rotate tự động | Không có sẵn | Có (Lambda rotation, tích hợp sẵn RDS) |

* Cả hai đều tích hợp ECS (`secrets` trong task definition → inject thành env var lúc start) và Lambda.
* **KMS**: quản lý key mã hóa; S3/EBS/RDS/SSM SecureString dùng KMS phía dưới. Backend hiếm khi gọi KMS trực tiếp, chỉ cần hiểu "data at rest được mã hóa bằng key trong KMS, quyền dùng key cũng là IAM".
* Lưu ý: secret inject qua env chỉ đọc lúc **task start** → đổi secret phải redeploy/force new deployment.

---

## 10. Observability

| Service | Dùng cho |
|---|---|
| CloudWatch Logs | Log app (ECS `awslogs` driver, Lambda tự đẩy). Logs Insights để query |
| CloudWatch Metrics | CPU/RAM task, request count, 5xx ALB, SQS queue depth, Lambda error/duration/throttle |
| CloudWatch Alarms | Cảnh báo khi metric vượt ngưỡng → SNS → Slack/email; trigger auto scaling |
| X-Ray / ADOT | Distributed tracing (tương tự Jaeger + OpenTelemetry trong CV) |
| CloudTrail | Audit **ai gọi API AWS nào, lúc nào** (ai xóa bucket, ai sửa SG) — khác với log app |

Metric nên alarm cho backend: ALB `HTTPCode_Target_5XX_Count`, `TargetResponseTime` p99, ECS CPU/Memory, SQS `ApproximateAgeOfOldestMessage` (consumer chậm), DLQ có message, Lambda `Errors`/`Throttles`, RDS CPU/connections/free storage.

---

## 11. CI/CD và chiến lược deploy

Luồng chuẩn với container:

```text
git push → GitHub Actions → build image → push ECR (tag = git SHA)
        → render task definition mới với image tag → update ECS service
        → ECS rolling update, ALB health check, circuit breaker rollback nếu fail
```

| Chiến lược | Cách | Ưu / nhược |
|---|---|---|
| Rolling (mặc định ECS) | Thay dần task cũ bằng task mới (`minimumHealthyPercent`/`maximumPercent`) | Đơn giản; có lúc 2 version chạy song song |
| Blue/Green (ECS + CodeDeploy, hoặc ECS native) | Dựng bộ mới đầy đủ, chuyển traffic ALB sang, giữ bộ cũ để rollback nhanh | Rollback tức thì; tốn tài nguyên gấp đôi khi deploy |
| Canary | Chuyển 10% traffic trước, ổn mới chuyển hết | An toàn nhất; cần metric tốt |

* Tag image bằng **git SHA**, không dùng `latest` → biết chính xác đang chạy code nào, rollback được.
* **Deployment circuit breaker** của ECS: task mới không healthy → tự rollback về task definition cũ.
* Lambda: version + alias, có thể shift traffic dần (canary) qua alias.
* IaC: Terraform/CloudFormation/CDK — backend chỉ cần hiểu "hạ tầng được mô tả bằng code, review qua PR, apply có kiểm soát".

---

## 12. Scale và High Availability

* **Stateless app**: session/cache/state để Redis/DB, file để S3 → scale ngang thoải mái, task chết không mất gì.
* **Auto Scaling**: ECS Service Auto Scaling (target tracking CPU 60%, hoặc request count per target), EC2 Auto Scaling Group, Lambda tự scale, DynamoDB on-demand.
* **Multi-AZ** ở mọi tầng: ALB trải nhiều subnet/AZ, ECS task ≥ 2 trải AZ, RDS Multi-AZ.
* **Scale theo queue**: worker scale theo độ sâu SQS thay vì CPU.
* **Thứ tự bottleneck** thường gặp khi scale: app → DB connection → DB write → cache hot key → service bên thứ 3.
* WebSocket nhiều instance: client A ở task 1, client B ở task 2 → cần **Redis Pub/Sub** (hoặc ElastiCache) để broadcast giữa các task, hoặc API Gateway WebSocket.

---

## 13. Chi phí — biết để không nói sai

* Thứ hay "đốt tiền" bất ngờ: **NAT Gateway** (giờ + GB), data transfer ra internet/cross-AZ, ALB chạy 24/7, RDS quên tắt, log CloudWatch không đặt retention, EBS snapshot cũ.
* EC2: On-Demand / Reserved hoặc Savings Plans (cam kết 1–3 năm, rẻ hơn) / **Spot** (rẻ rất nhiều nhưng có thể bị thu hồi → chỉ cho job chịu ngắt được).
* Fargate Spot cho worker không quan trọng.
* Lambda rẻ khi tải thấp/không đều; tải cao đều đặn 24/7 có thể đắt hơn container.
* Dùng tag (`project`, `env`) + Cost Explorer/Budgets để theo dõi.

---

## 14. Map từ CV sang câu hỏi phỏng vấn

| Dòng trong CV | Interviewer sẽ đào | Ý chính cần trả lời |
|---|---|---|
| Lambda + EventBridge cho daily job | Sao không dùng cron trong service? Job chạy quá 15 phút thì sao? Chạy trùng thì sao? | Không chiếm tài nguyên service, không bị chạy N lần khi service có N replica. Job dài → Step Functions / ECS task chạy theo schedule / chia nhỏ qua SQS. EventBridge là at-least-once → job idempotent (lock/dedup theo ngày) |
| S3 multipart + presigned URL | Vì sao không upload qua backend? Upload dở thì sao? Bảo mật? | Backend không chịu băng thông/RAM. Part fail retry riêng. Lifecycle rule abort incomplete multipart. URL hết hạn ngắn, key do backend sinh, giới hạn content-type/size |
| ECS | Fargate hay EC2? Task role vs execution role? Deploy không downtime thế nào? | Xem §4, §2, §11 |
| EC2 (U2U) | Sao chạy Docker Compose trên EC2? Scale thế nào? | Đơn giản, team nhỏ; giới hạn là scale tay, 1 máy = SPOF; hướng lên ECS/ASG + ALB |
| Redis caching | Trên AWS dùng gì? | ElastiCache Redis/Valkey, đặt trong private subnet, SG chỉ cho app |
| RabbitMQ | AWS có gì tương đương? | Xem §8 |
| Jaeger/OTel | Trên AWS? | X-Ray hoặc ADOT collector đẩy về X-Ray/vendor; vẫn dùng chuẩn OTel |
| Rate limit trên Redis | AWS có sẵn không? | API Gateway throttling/usage plan, WAF rate-based rule (theo IP); limit theo user/business vẫn làm ở app |

---

## 15. Câu hỏi hay gặp + trả lời mẫu

**1. Kiến trúc một web app chuẩn trên AWS?**

> Route 53 trỏ domain về CloudFront/ALB, ACM cấp HTTPS. Frontend static để S3 + CloudFront. API chạy ECS Fargate trong private subnet trên 2 AZ, sau ALB ở public subnet. Database RDS Multi-AZ, Redis ElastiCache, cả hai trong private subnet, SG chỉ cho app truy cập. Secret ở SSM/Secrets Manager, log/metric ở CloudWatch, CI/CD build image đẩy ECR rồi rolling update ECS.

**2. Private subnet ra internet thế nào? Gọi S3 từ private subnet có cần NAT không?**

> Ra internet qua NAT Gateway ở public subnet. Gọi S3/DynamoDB nên dùng Gateway VPC Endpoint — miễn phí và không đi qua NAT, vừa rẻ vừa an toàn hơn.

**3. Lambda hay ECS?**

> Lambda cho việc event-driven, ngắn (< 15 phút), tải không đều: S3 trigger, cron, webhook, glue. ECS cho API chạy liên tục, kết nối dài như WebSocket, cần kiểm soát runtime, tải đều — vì cold start, timeout và connection DB khi scale là điểm yếu của Lambda.

**4. SQS và SNS khác gì? Khi nào kết hợp?**

> SQS là queue, consumer pull, một message một consumer xử lý. SNS là pub/sub push tới nhiều subscriber. Kết hợp SNS → nhiều SQS để fan-out mà mỗi consumer vẫn có buffer và retry riêng.

**5. Message SQS bị xử lý 2 lần, vì sao và xử lý thế nào?**

> Standard queue là at-least-once, và nếu xử lý lâu hơn visibility timeout thì message hiện lại cho consumer khác. Đặt visibility timeout đủ dài, delete sau khi xử lý xong, và consumer phải idempotent bằng dedup key lưu trong DB/Redis. Cần thứ tự và dedup thì dùng FIFO.

**6. Lưu secret thế nào?**

> Không hard-code, không commit `.env`. Để ở SSM Parameter Store SecureString hoặc Secrets Manager nếu cần rotate. ECS inject vào env qua task execution role chỉ có quyền đọc đúng path đó.

**7. Làm sao deploy không downtime trên ECS?**

> Rolling update với minimumHealthyPercent 100, maximumPercent 200: ECS chạy task mới, đợi ALB health check pass rồi mới drain task cũ. Bật deployment circuit breaker để tự rollback. App phải xử lý graceful shutdown khi nhận SIGTERM.

**8. Multi-AZ khác Read Replica?** → §7.

**9. Security Group khác NACL?** → §3.

**10. S3 file private mà user cần xem?**

> Backend kiểm tra quyền rồi trả presigned GET URL thời hạn ngắn; hoặc CloudFront signed URL/cookie nếu cần cache.

**11. Ứng dụng chậm/lỗi trên AWS, debug từ đâu?**

> Xem CloudWatch metric ALB (5xx, latency) để biết lỗi ở tầng nào, ECS task có restart không (stopped reason), CloudWatch Logs của container, trace để tìm span chậm, rồi metric DB (CPU, connections, slow query). Nếu nghi ai đó đổi cấu hình hạ tầng thì xem CloudTrail.

**12. Sao không public trực tiếp port 8080 của backend?**

> Giảm attack surface: chỉ ALB public 443, TLS terminate ở ALB, backend chỉ nhận từ SG của ALB/frontend. Thêm WAF, rate limit ở một chỗ.

---

## 16. Thứ tự ôn đề xuất

1. §2 IAM + §3 VPC (nền tảng, dễ bị hỏi chéo).
2. §4 Compute + §5 Cửa vào → kể được [video-call deploy](../video-call/docs/interview_deploy_review.md) trơn tru.
3. §8 Messaging + §6 S3 → nối với kinh nghiệm RabbitMQ, multipart upload.
4. §7 Database, §9–§12.
5. Tự trả lời §15 thành tiếng, mỗi câu ≤ 1 phút.

Đọc thêm trong repo: [aws_core_concepts_for_backend_developers.md](../video-call/docs/aws_core_concepts_for_backend_developers.md), [route53_acm_alb_notes.md](../video-call/docs/route53_acm_alb_notes.md), [aws_ecs_architecture_questions.md](../video-call/docs/aws_ecs_architecture_questions.md).
