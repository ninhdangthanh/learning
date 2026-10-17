Mục tiêu cuối:

```text
                         Internet
                            │
                            ▼
                     ┌─────────────┐
                     │ API Gateway │
                     │ Auth/Rate   │
                     │ Routing     │
                     └──────┬──────┘
                            │
                            ▼
                     ┌─────────────┐
                     │     ALB     │
                     │   Routing   │
                     │ HealthCheck │
                     └──────┬──────┘
                            │
                    ┌───────┴────────┐
                    ▼                ▼
              ┌──────────┐     ┌──────────┐
              │  Order   │     │ Product  │
              │  ECS     │     │  ECS     │
              └──────────┘     └──────────┘
                 │  │              │  │
                #1 #2             #1 #2

                     API Gateway
                          │
                          └──────────► Lambda
```

Và bạn sẽ hiểu được **tại sao API Gateway không phải Load Balancer**, mà hai thằng có thể đứng cùng nhau.

---

# AWS Backend Project — API Gateway + ALB + ECS + Lambda

## 0. Project mục tiêu

Build một hệ thống:

> **Mini F&B Order System**

Có 2 service:

```text
Order Service
Product Service
```

Các API:

```http
GET    /products
GET    /products/{id}
POST   /products

GET    /orders
GET    /orders/{id}
POST   /orders
DELETE /orders/{id}
```

Tech stack:

```text
Go
Docker
AWS ECS Fargate
AWS ALB
AWS API Gateway
AWS Lambda
AWS CloudWatch
JWT
Không cần dùng database, chỉ fmt.print để biết nó hoạt động
```

---

# Phase 1 — Hiểu request flow

Trước tiên build Go API local.

```text
Client
  ↓
Order Service :8080
```

và:

```text
Client
  ↓
Product Service :8081
```

### Order Service

Implement:

```http
GET    /orders
GET    /orders/:id
POST   /orders
DELETE /orders/:id
GET    /health
```

### Product Service

Implement:

```http
GET    /products
GET    /products/:id
POST   /products
GET    /health
```

### Mục tiêu

Bạn phải hiểu rõ:

```text
HTTP request
    ↓
Router
    ↓
Handler
    ↓
Service
    ↓
Repository
    ↓
MongoDB
```

Không cần AWS ở phase này.

---

# Phase 2 — Dockerize

Dockerize cả 2 service.

```text
docker-compose

order-service     :8080
product-service   :8081
mongodb           :27017
```

Test:

```text
GET http://localhost:8080/orders
GET http://localhost:8081/products
```

### Học

* Dockerfile
* Docker image
* container
* port mapping
* environment variables
* Docker network

### Mục tiêu

Bạn phải hiểu:

```text
Docker Image
     ↓
Container
     ↓
HTTP Server
```

---

# Phase 3 — ECS Fargate

Đưa **Order Service** lên ECS trước.

Architecture:

```text
Internet
   ↓
ECS
   ↓
Order Service
```

Không ALB trước.

Bạn cần hiểu:

```text
ECS Cluster
   ↓
Task Definition
   ↓
Task
   ↓
Container
```

Sau đó deploy:

```text
Order Service
    ↓
ECS Task #1
```

Test trực tiếp endpoint của ECS.

---

# Phase 4 — ECS chạy nhiều instance

Đây là phase cực kỳ quan trọng cho ALB.

Scale:

```text
Order Service

ECS Task #1
ECS Task #2
ECS Task #3
```

Nhưng lúc này bạn sẽ gặp vấn đề:

> Client gọi task nào?

Đừng giải quyết bằng cách cho client biết 3 IP.

Đây chính là lý do cần **Load Balancer**.

---

# Phase 5 — ALB

Architecture:

```text
Internet
   │
   ▼
  ALB
   │
   ├──── ECS #1
   ├──── ECS #2
   └──── ECS #3
```

Học theo thứ tự:

### 5.1 Load Balancer

Tạo:

```text
Application Load Balancer
```

### 5.2 Listener

Ví dụ:

```text
ALB
 └── Listener :80
```

### 5.3 Target Group

```text
Target Group
 ├── ECS #1
 ├── ECS #2
 └── ECS #3
```

### 5.4 Health Check

Tạo:

```http
GET /health
```

ALB:

```text
ALB
 │
 ├── /health → ECS #1 ✅
 ├── /health → ECS #2 ✅
 └── /health → ECS #3 ❌
```

ALB sẽ ngừng gửi traffic đến task unhealthy.

### 5.5 Test

Gọi:

```http
GET http://<ALB>/orders
```

Sau đó stop:

```text
ECS #2
```

và test lại.

---

# Phase 6 — ALB Routing

Bây giờ đưa Product Service vào.

Architecture:

```text
                    ALB
                     │
             ┌───────┴────────┐
             │                │
             ▼                ▼
       /orders/*         /products/*
             │                │
             ▼                ▼
       Order Service     Product Service
          ECS #1             ECS #1
          ECS #2             ECS #2
```

ALB rules:

```text
/orders/*     → Order Target Group

/products/*   → Product Target Group
```

Đây là phần bạn cần hiểu rất rõ.

### Học

* Listener
* Listener Rule
* Target Group
* Path-based routing
* Host-based routing
* Priority
* Health Check

Sau phase này:

```text
GET /orders
      ↓
ALB
      ↓
Order Service

GET /products
      ↓
ALB
      ↓
Product Service
```

---

# Phase 7 — API Gateway

**Đây mới là lúc đưa API Gateway vào.**

Architecture:

```text
Internet
   ↓
API Gateway
   ↓
ALB
   ↓
ECS
```

API Gateway trở thành **public API entry point**.

Client không gọi ALB trực tiếp nữa.

```text
Client
  │
  │ GET /orders
  ▼
API Gateway
  │
  ▼
ALB
  │
  ▼
Order Service
```

---

# Phase 8 — API Gateway Routing

Configure:

```text
GET    /orders
GET    /orders/{id}
POST   /orders
DELETE /orders/{id}

GET    /products
GET    /products/{id}
POST   /products
```

Bạn cần hiểu:

```text
API Gateway Route
       ↓
Integration
       ↓
ALB HTTP endpoint
```

Ví dụ:

```text
API Gateway

GET /orders
       ↓
http://ALB/orders

GET /products
       ↓
http://ALB/products
```

---

# Phase 9 — API Gateway → Lambda

Không phải request nào cũng cần ECS.

Tạo một Lambda nhỏ:

```text
GET /health
```

Architecture:

```text
                 API Gateway
                  /       \
                 /         \
                ▼           ▼
              ALB         Lambda
               │
               ▼
              ECS
```

Ví dụ:

```text
GET /orders
    ↓
API Gateway
    ↓
ALB
    ↓
ECS
```

nhưng:

```text
GET /system/info
    ↓
API Gateway
    ↓
Lambda
```

Lambda trả:

```json
{
  "service": "order-system",
  "version": "1.0.0",
  "environment": "production"
}
```

### Mục tiêu

Bạn phải hiểu **Integration** của API Gateway:

```text
API Gateway
 ├── HTTP Integration → ALB
 └── Lambda Integration → Lambda
```

Đây là một trong những kiến thức quan trọng nhất của project.

---

# Phase 10 — Authentication / JWT

Bây giờ mới thêm authentication.

Flow:

```text
Client
   │
   │ Authorization: Bearer <JWT>
   ▼
API Gateway
   │
   ▼
Auth
   │
   ▼
ALB
   │
   ▼
ECS
```

Ví dụ:

```http
GET /orders
Authorization: Bearer eyJ...
```

Bạn cần học:

### JWT

Hiểu:

```text
Header
Payload
Signature
```

Ví dụ payload:

```json
{
  "sub": "user-123",
  "role": "admin",
  "exp": 1780000000
}
```

### API Gateway Authorization

Tìm hiểu:

* JWT authorizer
* issuer
* audience
* token validation
* unauthorized request

Mục tiêu:

```text
No JWT
   ↓
401

Invalid JWT
   ↓
401

Valid JWT
   ↓
ALB
   ↓
ECS
```

---

# Phase 11 — Throttling / Rate Limiting

Đây là phần tiếp theo.

Giả sử:

```text
Client
   ↓
API Gateway
```

Client spam:

```text
10,000 requests/sec
```

Bạn không muốn:

```text
API Gateway
     ↓
10,000 req/s
     ↓
ECS
     💥
```

Configure throttling:

```text
API Gateway
      ↓
Rate limit
      ↓
Backend
```

Ví dụ học concept:

```text
Rate:   100 req/s
Burst:  200
```

Sau đó test bằng:

```text
k6
```

Bạn đã dùng k6 rồi nên phần này khá phù hợp.

Test:

```text
10 VUs
50 VUs
100 VUs
```

Quan sát:

```text
2xx
4xx
5xx
latency
```

Đặc biệt phân biệt:

```text
API Gateway throttling
        vs
Application-level rate limiting
```

---

# Phase 12 — Monitoring

Cuối project thêm observability.

Architecture:

```text
Client
  ↓
API Gateway
  ↓
ALB
  ↓
ECS
  ↓
MongoDB
```

Theo dõi:

```text
API Gateway
 ├── Requests
 ├── 4xx
 ├── 5xx
 └── Latency

ALB
 ├── Request count
 ├── Target response time
 ├── Healthy targets
 └── Unhealthy targets

ECS
 ├── CPU
 ├── Memory
 └── Task count
```

Đưa log vào:

```text
CloudWatch Logs
```

Mục tiêu cuối:

> Có request lỗi thì biết lỗi xảy ra ở API Gateway, ALB hay ECS.

---

# Phase 13 — Final Architecture

Sau toàn bộ project, architecture của bạn sẽ là:

```text
                         INTERNET
                            │
                            ▼
                   ┌─────────────────┐
                   │  API GATEWAY    │
                   │                 │
                   │ • Routing       │
                   │ • JWT Auth      │
                   │ • Throttling    │
                   │ • Integration   │
                   └────────┬────────┘
                            │
              ┌─────────────┴─────────────┐
              │                           │
              ▼                           ▼
       HTTP Integration            Lambda Integration
              │                           │
              ▼                           ▼
       ┌─────────────┐               Lambda
       │     ALB     │
       │             │
       │ • Routing   │
       │ • Health    │
       │ • Balancing │
       └──────┬──────┘
              │
       ┌──────┴────────┐
       │               │
       ▼               ▼
   /orders/*       /products/*
       │               │
       ▼               ▼
 ┌───────────┐    ┌───────────┐
 │   Order   │    │  Product  │
 │    ECS    │    │    ECS    │
 └─────┬─────┘    └─────┬─────┘
       │                │
   ┌───┼───┐        ┌───┼───┐
   ▼   ▼   ▼        ▼   ▼   ▼
  ECS ECS ECS       ECS ECS ECS
   #1  #2  #3        #1  #2  #3
       │                │
       └───────┬────────┘
               ▼
          MongoDB Atlas
```

---

# Những kiến thức bạn phải nắm sau project

Mình sẽ chia thành **Must Know** và **Nice to Know**.

## 🔴 Must Know

### API Gateway

* API Gateway là gì
* API Gateway vs ALB
* Route
* HTTP method
* Path parameter
* Query parameter
* Integration
* HTTP integration
* Lambda integration
* JWT authentication
* Authorization
* Throttling
* Rate limiting
* 4xx / 5xx
* API Gateway logs/metrics

### ALB

* ALB là gì
* Listener
* Listener Rule
* Target Group
* Target
* Health Check
* Path-based routing
* Host-based routing
* Load balancing
* ECS integration
* Healthy / unhealthy target
* Deregistration

### ECS

* Cluster
* Task Definition
* Task
* Service
* Desired count
* Scaling
* Container
* Port mapping

---

# 🟡 Chưa cần học ngay

Bạn **chưa cần** nhảy vào:

* API Gateway WebSocket
* API Gateway REST API nâng cao
* AWS Cognito quá sâu
* AWS WAF
* Global Accelerator
* CloudFront
* NLB
* GWLB
* ECS EC2 launch type
* EKS
* Service Mesh

Những thứ này để sau.

---

# Project roadmap gọn nhất

Nếu mình biến thành checklist học thực tế:

```text
WEEK 1
────────────────────────
[ ] Go Order Service
[ ] Go Product Service
[ ] REST API
[ ] Health Check
[ ] Docker
[ ] Docker Compose


WEEK 2
────────────────────────
[ ] ECS Cluster
[ ] Task Definition
[ ] ECS Task
[ ] ECS Service
[ ] Deploy Order Service
[ ] Scale 3 Tasks

[ ] ALB
[ ] Listener
[ ] Target Group
[ ] Health Check
[ ] ALB → ECS

[ ] ALB Path Routing
[ ] /orders → Order ECS
[ ] /products → Product ECS


WEEK 3
────────────────────────
[ ] API Gateway
[ ] Routes
[ ] HTTP Integration
[ ] API Gateway → ALB
[ ] API Gateway → Lambda

[ ] JWT
[ ] JWT Authorizer
[ ] Authentication
[ ] Authorization

[ ] Throttling
[ ] Rate limit
[ ] k6 load test

[ ] CloudWatch
[ ] Logs
[ ] Metrics
```

## Và quan trọng nhất: học theo câu hỏi

Trong project này, mỗi khi học một component, bạn nên tự trả lời được:

> **"Nếu bỏ component này đi thì hệ thống mất cái gì?"**

Ví dụ:

**Bỏ ALB:**

```text
API Gateway
    ↓
ECS #1
```

→ Không còn load balancing giữa nhiều ECS tasks.

**Bỏ API Gateway:**

```text
Internet
    ↓
ALB
    ↓
ECS
```

→ Vẫn chạy bình thường, nhưng mất lớp API management/entry point như JWT authorization và throttling ở API Gateway.

**Bỏ ECS:**

```text
API Gateway
    ↓
ALB
    ↓
???
```

→ Không có application container để xử lý request.

**Bỏ Lambda:**

→ Hệ thống vẫn chạy; Lambda chỉ là **một loại integration/backend target khác** mà API Gateway có thể gọi.

Nếu bạn build project đúng theo thứ tự trên, cuối cùng bạn sẽ không chỉ nhớ "API Gateway là API management, ALB là load balancing", mà sẽ **nhìn vào architecture và biết chính xác request đi qua từng layer như thế nào**.
