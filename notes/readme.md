# Backend Interview Notes

Thư mục này là bộ notes ôn phỏng vấn Middle Backend theo CV hiện tại: Golang, Bun/TypeScript, PostgreSQL, MongoDB, Redis, RabbitMQ, gRPC, microservices, AWS và system design.

`readme.md` chỉ dùng để giới thiệu, định hướng plan và trỏ đến các file topic. Kiến thức chi tiết nằm trong từng file riêng để dễ ôn, dễ bổ sung và tránh bị biến thành một file quá dài.

---

## Plan chính

1. [Kế hoạch ôn Middle Backend Go - 3 tuần](middle-backend-interview-plan.md)
2. Tuần 1: đóng gói Redis clone và RAG thành project pitch/deep dive.
3. Tuần 2: Go/API/PostgreSQL/Redis và project deep dive.
4. Tuần 3: MongoDB, RabbitMQ, gRPC, microservice và system design.

---

## Core language

| File | Dùng để ôn |
|---|---|
| [Golang Core Interview Notes](golang-core-interview.md) | Interface, pointer/value receiver, goroutine, channel, context, memory, error handling, gRPC/RabbitMQ trong Go |
| [TypeScript Core Interview Notes](typescript-core-interview.md) | Bun/Node.js runtime, event loop, callback/promise/async-await, runtime validation, type narrowing, React TS practical |

---

## Database, cache và storage

| File | Dùng để ôn |
|---|---|
| [Database Middle Roadmap](database-middle-roadmap.md) | PostgreSQL/index/query plan/transaction/MVCC/partition/replication/connection pool/cache/migration |
| [Redis Middle Notes](redis-middle-notes.md) | Redis use cases, data types, JWT blacklist, sorted-set scheduler, cache invalidation, edge cases và cache strategies |
| [MongoDB F&B Notes](mongo_fnb.md) | Document model, embed/reference, hot document, aggregation, index, sharding, transaction, TTL, change stream |
| [Production Backend Concepts](production-backend-concepts.md) | Các tình huống production dễ bị hỏi: pagination, idempotency, cache stampede, retry storm, online migration, read replica stale |
| [Elasticsearch Middle Notes](elasticsearch-middle-notes.md) | Inverted index, analyzer, `text` vs `keyword`, query DSL (query vs filter context), BM25 relevance, aggregation, sync DB→ES, reindex bằng alias, edge case production |
| [Elasticsearch Stack Implement Plan](../elasticsearchStack/elasticsearch-implement-plan.md) | Plan dựng ES + Kibana bằng Docker và thực hành theo phase: index/mapping, query DSL, aggregation, dashboard, sync DB→ES |

Concept nên nắm thêm trong nhóm này:

* PostgreSQL: `EXPLAIN ANALYZE`, composite index, row/table lock, `SELECT ... FOR UPDATE`, MVCC, isolation level, read replica lag, online migration, connection pool exhaustion.
* Race condition/isolation: lost update và vì sao Read Committed không chặn, write skew và vì sao `FOR UPDATE` không cứu được, phantom read, khác biệt Repeatable Read giữa PostgreSQL và MySQL, chọn giữa atomic `UPDATE` có guard / `FOR UPDATE` / optimistic version / unique constraint, `SKIP LOCKED` cho job queue.
* Compound index: B-tree sort theo tuple, leftmost prefix rule, thứ tự viết `WHERE` không quan trọng nhưng tập cột thì có, index dừng seek ở cột range đầu tiên, quy tắc ESR, `Index Cond` vs `Filter` khi đọc plan.
* Khi nào dùng compound index: một compound `(A,B)` vs hai index rời `(A)` và `(B)` và vì sao BitmapAnd chậm hơn, multi-tenant, filter kèm `ORDER BY LIMIT`, covering index, unique trên tổ hợp cột; khi nào không nên.
* Redis/cache: cache-aside, invalidation, TTL jitter, hot key, stampede, penetration, JWT blacklist, sorted-set scheduler, Redis Streams, distributed lock và fencing token.
* Redis interview: tại sao nhanh (RAM + data structure + single-thread), giảm tải DB bằng cách nào và bằng con số nào, cache cái gì với TTL bao nhiêu, phân biệt penetration/breakdown/avalanche, Redis down thì hệ thống ra sao.
* MongoDB transaction: Mongo có ACID transaction từ 4.0 nhưng ít dùng vì embedding làm single-document write đã atomic sẵn; chi phí thật (replica set bắt buộc, giới hạn 60s, cache pressure, oplog, two-phase commit xuyên shard, app phải tự retry); các cơ chế thay thế: atomic operator, unique index, optimistic version, idempotency key, saga.
* MongoDB: MongoDB vs PostgreSQL trade-off, schema governance, aggregation memory limit, unbounded array, document relocation, write concern, replica lag.
* Elasticsearch: inverted index vs `LIKE`, analyzer (index và query cùng analyzer), `text` vs `keyword`, query context vs filter context, BM25 (TF/IDF/field length), aggregation bucket/metric, ES là secondary store, sync DB→ES (dual write/outbox/CDC), deep pagination với `search_after`, reindex zero-downtime bằng alias.

---

## Service communication, queue và API

| File | Dùng để ôn |
|---|---|
| [Backend Communication Roadmap](backend-communication-roadmap.md) | Roadmap tổng quan cho REST/gRPC/queue, retry/backoff, circuit breaker, outbox, API versioning, WebSocket scale |
| [gRPC Middle Notes](grpc-middle-notes.md) | gRPC cho Middle Backend: protobuf, deadline, status code, interceptor, streaming, compatibility, observability |
| [RabbitMQ Middle Notes](rabbitmq-middle-notes.md) | RabbitMQ đủ sâu cho Middle Backend: exchange types, prefetch, DLX/DLQ, retry, ordering, reliability, outbox |
| [Backend Security Middle Notes](backend-security-middle.md) | Rainbow table, brute-force login, scraping/bot abuse, app-level DDoS, SQL/NoSQL injection, CORS, API key, webhook signature, file upload security |
| [JWT And Session Middle Notes](jwt-session-middle-notes.md) | JWT/session production: blacklist, token versioning, refresh token rotation, revoke, multi-device sessions |
| [Rate Limit](../rate-limit) | Concept, use case, edge case production, 5 thuật toán phổ biến và code Go: fixed window, sliding window log/counter, token bucket, leaky bucket |
| [Notebook - Architecture](notebook.md) | Microservices, DDD, Saga, CQRS, EDA, circuit breaker, service discovery, API-led architecture, Kafka overview |
| [CQRS Demo](cqrs-demo/README.md) | Demo Go nhỏ: tách write model và read model, nối nhau bằng event; không DB, không saga |
| [Saga Demo](saga-demo/README.md) | Demo Go nhỏ: saga orchestration, nghiệp vụ nhiều bước xuyên service và compensating action khi fail giữa chừng |
| [Event-Driven Architecture Notes](event-driven-architecture.md) | EDA chuyên sâu: event vs command, event notification/state transfer/sourcing, CQRS, choreography vs orchestration, delivery guarantee, ordering, idempotent consumer, outbox, schema evolution, chọn broker, failure mode |

Concept nên nắm thêm trong nhóm này:

* RabbitMQ: exchange, routing key, queue binding, ack/nack, prefetch, retry, DLQ, poison message, at-least-once delivery, idempotent consumer.
* gRPC: protobuf field compatibility, unary/server-stream/client-stream/bidi-stream, deadline, status code, interceptor, REST vs gRPC trade-off.
* API production: rate limit nhiều dimension, idempotency key, request timeout, circuit breaker, graceful shutdown, API/backend versioning, backward compatibility.
* EDA: event vs command, at-least-once + idempotent consumer, ordering theo partition key, dual-write và transactional outbox, schema evolution, choreography vs orchestration, poison message/DLQ, correlation id để trace luồng phân tán.
* JWT/session: access token ngắn hạn, refresh token rotation, `jti` blacklist, token versioning, logout current/all devices, session audit.
* Backend security: password hashing với salt, chống brute-force theo nhiều dimension, anti-scraping, CORS đúng vai trò, SQL/NoSQL injection, webhook signature, upload file an toàn.
* Rate limit: chọn thuật toán theo mục tiêu, ví dụ token bucket cho burst/API gateway, sliding window log cho login, sliding window counter cho public API, leaky bucket cho downstream/worker protection.

---

## System design và scale

| File | Dùng để ôn |
|---|---|
| [Scale System Questions](scale_system_question.md) | DNS, CDN, WAF, load balancer, API gateway, stateless app, autoscaling, cache, queue, database, sharding, observability |
| [Production Backend Concepts](production-backend-concepts.md) | Checklist failure mode theo từng topic để đưa vào system design answer |
| [Production Scale Metrics](production-scale-metrics.md) | Map số liệu production theo CV: users, CCU, RPS/TPS, latency, data volume, throughput và mức độ bạn trực tiếp chạm |
| [Backend Classic Questions](backend-classic-questions.md) | Câu hỏi tình huống kinh điển: API chậm, DB CPU 100%, connection pool, traffic tăng 10x, khi nào thêm index, khi nào dùng queue, sync vs async, service downstream chậm |

Khi luyện system design, luôn đi theo thứ tự:

1. Requirements và traffic estimate.
2. API và data model.
3. Read path, write path, async path.
4. Bottleneck và failure mode.
5. Trade-off và scale path.

Concept nên nắm thêm trong nhóm này:

* Scale dọc: tăng CPU/RAM cho một instance, đơn giản nhưng có giới hạn vật lý và dễ thành single point of failure.
* Scale ngang: tăng số lượng instances sau load balancer/API gateway, cần stateless app, shared DB/cache/queue, health check và autoscaling.
* Scale backend không chỉ là thêm app instances; database/cache/queue có thể trở thành bottleneck tiếp theo.

---

## Infrastructure, container và AWS

| File | Dùng để ôn |
|---|---|
| [Kubernetes Middle Notes](kubernetes-middle-notes.md) | K8s cho backend chạy trên EKS: Cluster/Control Plane/Node, Pod, Deployment/replica, Ingress + ALB, Helm `values.yaml`, câu trả lời "có kinh nghiệm K8s không" |
| [AWS Middle Notes](aws-middle-notes.md) | AWS cho backend đi phỏng vấn: IAM/VPC/SG, chọn EC2 vs ECS vs Lambda, ALB vs NLB vs API Gateway, S3, RDS vs DynamoDB, SQS/SNS/EventBridge, secret, CloudWatch, CI/CD, map từ CV sang câu hỏi |
| [Video Call Deploy Review](../video-call/docs/interview_deploy_review.md) | Hạ tầng ECS Fargate + ALB + Route 53 + ACM đang chạy, cách pitch, 14 điểm yếu demo-grade và cách production sẽ làm |

Concept nên nắm thêm trong nhóm này:

* Kubernetes: container orchestration, Control Plane vs Worker Node, Pod là đơn vị deploy nhỏ nhất, Deployment giữ đúng số replica + rolling update/rollback, Ingress route traffic từ ALB vào service.
* EKS: AWS quản Control Plane, Node thường là EC2 instance, backend chỉ chỉnh Helm `values.yaml` (image tag, replicaCount, env) rồi deploy qua CI/CD.

---

## Project pitch/deep dive

| Project | Nên nhấn mạnh |
|---|---|
| Redis clone | RESP parser, command dispatch, TTL, AOF/RDB concept, Pub/Sub, concurrency, tests, phần còn thiếu so với Redis thật |
| RAG chatbot/backend-AI | Upload, ingestion worker, chunking, vector search, Qdrant, Postgres metadata, Redis worker/cache, citation, timeout/retry/failure mode |

Output nên có trước khi phỏng vấn:

* Pitch 60-90 giây cho mỗi project.
* 8-10 câu Q&A deep dive cho mỗi project.
* 2 câu chuyện STAR từ kinh nghiệm làm việc thật.
* 2 bài system design luyện trong 45 phút.
* 1 bảng production metrics đã điền số thật hoặc ghi rõ phần nào chưa sở hữu metric.

---

## Cách dùng notes hằng ngày

1. Mở [middle-backend-interview-plan.md](middle-backend-interview-plan.md) để biết hôm nay ôn gì.
2. Mở đúng file topic, đọc 60-80 phút.
3. Tự trả lời thành tiếng 30-40 phút.
4. Ghi lại câu hỏi chưa chắc vào `notes/middle-interview-qa.md`.
5. Chỉ làm mini demo khi một concept còn mơ hồ; không mở thêm project lớn trước phỏng vấn.
