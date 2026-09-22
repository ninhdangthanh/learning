# Project — Production-Ready Realtime Chat Server with Go WebSocket

## 1. Project Overview

Xây dựng một **realtime chat server bằng Golang**, sử dụng WebSocket, hỗ trợ:

* Multiple concurrent WebSocket connections
* User authentication bằng JWT
* Room-based messaging
* Multiple connections trên cùng một user
* Connection lifecycle management
* Ping/Pong & heartbeat
* Graceful disconnect
* Concurrent message broadcasting
* Room authorization
* Connection cleanup
* Graceful shutdown

### High-level architecture

```text
                         ┌──────────────┐
                         │   Browser    │
                         └──────┬───────┘
                                │
                           WebSocket
                                │
                                ▼
                    ┌─────────────────────┐
                    │    Go HTTP Server   │
                    │                     │
                    │ WebSocket Handler   │
                    └──────────┬──────────┘
                               │
                     Authenticate JWT
                               │
                               ▼
                    ┌─────────────────────┐
                    │  Connection Manager │
                    │                     │
                    │ User → Connections  │
                    │ Room → Clients      │
                    └──────────┬──────────┘
                               │
                    ┌──────────┴──────────┐
                    ▼                     ▼
               Room Manager          Client Manager
                    │                     │
             ┌──────┼──────┐       ┌──────┼──────┐
             ▼      ▼      ▼       ▼      ▼      ▼
           Room A Room B Room C   User A User B User C
```

---

# 2. Main Goal

> Build a realtime chat system that demonstrates a practical understanding of WebSocket lifecycle, Go concurrency, connection management, authentication, authorization and realtime message delivery.

Không chỉ mục tiêu là **"WebSocket echo được message"**.

Mục tiêu cuối cùng là bạn có thể giải thích được:

```text
TCP
 ↓
HTTP
 ↓
HTTP Upgrade
 ↓
WebSocket
 ↓
Connection lifecycle
 ↓
Goroutine
 ↓
Channel
 ↓
Hub / Connection Manager
 ↓
Room
 ↓
Authentication
 ↓
Authorization
 ↓
Heartbeat
 ↓
Graceful shutdown
```

---

# 3. Scope

## Phase 1 — WebSocket Fundamentals

Implement basic WebSocket connection:

```text
Browser
   │
   │ HTTP Upgrade
   ▼
Go Server
   │
   │ 101 Switching Protocols
   ▼
WebSocket Connection
```

Endpoint:

```http
GET /ws
```

Client:

```json
{
  "message": "hello"
}
```

Server:

```json
{
  "message": "hello"
}
```

### Features

* HTTP → WebSocket upgrade
* Read message
* Write message
* Text message
* Binary message
* Detect disconnect
* Handle malformed messages
* Handle WebSocket errors
* Ping/Pong
* Connection cleanup

### Technical goal

Understand:

* WebSocket handshake
* WebSocket frames
* TCP connection vs WebSocket connection
* Full-duplex communication
* Connection lifecycle
* Ping/Pong
* Close frame
* Read/write behavior

---

# 4. Phase 2 — Concurrent Chat

Transform Echo Server → Chat Server.

```text
                    ┌── Client A
                    │
WebSocket Server ───┼── Client B
                    │
                    └── Client C
```

Implement:

```go
type Client struct {
    Conn *websocket.Conn
    Send chan []byte
}

type Hub struct {
    Clients    map[*Client]bool
    Register   chan *Client
    Unregister chan *Client
    Broadcast  chan []byte
}
```

### Flow

```text
Client A
   │
   │ send message
   ▼
WebSocket Handler
   │
   ▼
Hub.Broadcast
   │
   ├──────────► Client B
   ├──────────► Client C
   └──────────► Client D
```

### Client lifecycle

```text
Connect
   ↓
Register
   ↓
Read messages
   ↓
Broadcast
   ↓
Disconnect
   ↓
Unregister
   ↓
Cleanup
```

---

# 5. Phase 3 — Room

Introduce rooms.

Example:

```text
room-123
room-456
room-789
```

A client can join:

```text
User A
 ├── room-123
 └── room-456
```

### Message

```json
{
  "type": "message",
  "room_id": "room-123",
  "content": "Hello"
}
```

### Commands

```text
JOIN_ROOM
LEAVE_ROOM
SEND_MESSAGE
```

Example:

```json
{
  "type": "join_room",
  "room_id": "room-123"
}
```

Then:

```json
{
  "type": "message",
  "room_id": "room-123",
  "content": "Hello"
}
```

Only members of:

```text
room-123
```

receive the message.

---

# 6. Phase 4 — Authentication & Authorization

Add JWT authentication.

```text
Client
  │
  │ JWT
  ▼
WebSocket Handshake
  │
  ▼
Validate JWT
  │
  ├── invalid → Reject
  │
  └── valid
       │
       ▼
   userID = 123
       │
       ▼
   Connection
```

You should be able to explain:

> At which point is authentication performed?

and:

> Why shouldn't authorization simply be considered authentication?

---

## Authentication

JWT contains:

```json
{
  "sub": "user-123",
  "exp": 123456789
}
```

Server extracts:

```go
userID := claims.Subject
```

and associates:

```text
WebSocket Connection
        ↓
      userID
```

---

# 7. Phase 5 — Authorization

Add room membership.

Example:

```text
User A
   │
   ├── restaurant-001 ✓
   └── restaurant-002 ✗
```

When:

```json
{
  "type": "join_room",
  "room_id": "restaurant-002"
}
```

Server checks:

```text
Is User A a member of restaurant-002?
```

If not:

```json
{
  "type": "error",
  "code": "FORBIDDEN"
}
```

This makes the project much closer to a real backend system.

---

# 8. Phase 6 — Multiple Connections per User

This is an important production concept.

One user can have:

```text
              user-123
                  │
        ┌─────────┼─────────┐
        ▼         ▼         ▼
      Laptop    Mobile    Tablet
        │         │         │
       WS         WS        WS
```

Therefore don't design:

```go
map[userID]*Client
```

Instead:

```go
map[userID]map[*Client]bool
```

or conceptually:

```text
userID
  ↓
[]connections
```

Example:

```text
user-123
 ├── connection-1
 ├── connection-2
 └── connection-3
```

Now you can support:

```text
User sends message
        ↓
broadcast to room
        ↓
all eligible connections receive it
```

---

# 9. Phase 7 — Heartbeat & Connection Health

Implement:

```text
Ping
 ↓
Pong
 ↓
Connection alive
```

If client stops responding:

```text
Ping
 ↓
timeout
 ↓
connection considered dead
 ↓
cleanup
```

You should implement:

* Ping interval
* Pong handler
* Read deadline
* Write deadline
* Connection timeout
* Close dead connections

This is where you start understanding why simply having:

```go
conn.ReadMessage()
```

is not enough for production.

---

# 10. Phase 8 — Graceful Shutdown

When server receives:

```text
SIGTERM
```

it should not immediately kill all WebSocket connections.

Expected flow:

```text
SIGTERM
   ↓
Stop accepting new connections
   ↓
Notify existing connections
   ↓
Close WebSocket connections
   ↓
Wait for goroutines
   ↓
Shutdown HTTP server
```

Implement:

```go
context.Context
sync.WaitGroup
```

and graceful cleanup.

---

# 11. API / Protocol

Instead of REST-style endpoints such as:

```text
/send
/join
/leave
```

the WebSocket connection can use message types.

### Client → Server

```json
{
  "type": "join_room",
  "room_id": "room-123"
}
```

```json
{
  "type": "leave_room",
  "room_id": "room-123"
}
```

```json
{
  "type": "message",
  "room_id": "room-123",
  "content": "Hello"
}
```

### Server → Client

```json
{
  "type": "message",
  "room_id": "room-123",
  "sender_id": "user-123",
  "content": "Hello"
}
```

Error:

```json
{
  "type": "error",
  "code": "FORBIDDEN",
  "message": "You cannot join this room"
}
```

---

# 12. Acceptance Criteria

## WebSocket

* [ ] Client can establish a WebSocket connection.
* [ ] Server correctly handles HTTP Upgrade.
* [ ] Client can send text messages.
* [ ] Client can send binary messages.
* [ ] Server can send messages back.
* [ ] Server detects client disconnection.
* [ ] Server handles malformed WebSocket messages.
* [ ] Server properly closes connections.
* [ ] Ping/Pong heartbeat is implemented.

## Concurrency

* [ ] Multiple clients can connect simultaneously.
* [ ] One client's message can be broadcast to multiple clients.
* [ ] Concurrent clients do not corrupt shared state.
* [ ] Connection registration/unregistration is concurrency-safe.
* [ ] No data race is reported by `go test -race`.

## Rooms

* [ ] Client can join a room.
* [ ] Client can leave a room.
* [ ] Messages are only delivered to room members.
* [ ] A client can belong to multiple rooms.
* [ ] Empty rooms are cleaned up.

## Authentication

* [ ] Invalid JWT is rejected.
* [ ] Expired JWT is rejected.
* [ ] Valid JWT creates an authenticated connection.
* [ ] `userID` is associated with the WebSocket connection.

## Authorization

* [ ] User cannot join unauthorized rooms.
* [ ] User cannot send messages to unauthorized rooms.
* [ ] Authorization is checked server-side.

## Multiple connections

* [ ] One user can have multiple WebSocket connections.
* [ ] Disconnecting one connection does not disconnect other connections.
* [ ] User connection state is cleaned up correctly.

## Reliability

* [ ] Dead connections are detected.
* [ ] Ping/Pong timeout works.
* [ ] Server handles abnormal client disconnect.
* [ ] Server performs graceful shutdown.
* [ ] No goroutine leak is observed during normal connection lifecycle.

---

# 13. Technical Goals

Sau project này, bạn nên đạt được các technical goals sau:

### WebSocket

* Understand HTTP Upgrade.
* Understand WebSocket framing.
* Understand full-duplex communication.
* Understand connection lifecycle.
* Understand close/error behavior.
* Understand heartbeat.

### Go concurrency

* Goroutine lifecycle.
* Channel communication.
* Buffered vs unbuffered channel.
* `select`.
* Mutex vs channel-based synchronization.
* `context.Context`.
* `sync.WaitGroup`.
* Race detector.

### Concurrent architecture

Understand why we separate:

```text
HTTP Handler
      ↓
Client
      ↓
Hub
      ↓
Room
```

instead of putting everything inside one handler.

### Connection management

Understand:

```text
user
 ↓
connections
 ↓
rooms
 ↓
message routing
```

### Reliability

Understand:

* Backpressure
* Dead connection
* Write timeout
* Read timeout
* Heartbeat
* Cleanup
* Graceful shutdown
* Goroutine leak

---

# 14. Knowledge Goals

Sau project này, bạn phải **tự explain được**, không chỉ code được.

### WebSocket

1. WebSocket là gì?
2. WebSocket khác HTTP thế nào?
3. WebSocket khác TCP thế nào?
4. WebSocket handshake hoạt động thế nào?
5. HTTP Upgrade là gì?
6. WebSocket nằm ở layer nào của OSI?
7. Một WebSocket connection có thể gửi bao nhiêu message?
8. Vì sao WebSocket có thể two-way communication?
9. Ping/Pong dùng để làm gì?
10. Close frame hoạt động thế nào?

### TCP

11. TCP connection là gì?
12. WebSocket chạy trên TCP như thế nào?
13. TCP connection bị mất thì WebSocket biết bằng cách nào?
14. TCP disconnect khác application-level disconnect thế nào?

### Go

15. Vì sao cần goroutine cho mỗi connection?
16. Vì sao cần channel?
17. Khi nào dùng Mutex?
18. Khi nào dùng channel?
19. Buffered channel có tác dụng gì?
20. `select` hoạt động thế nào?
21. Làm sao detect goroutine leak?
22. `context.Context` dùng để làm gì?
23. `sync.WaitGroup` giải quyết vấn đề gì?

### Concurrency

24. Race condition xảy ra ở đâu?

Ví dụ:

```go
clients[client] = true
```

và:

```go
delete(clients, client)
```

được thực hiện đồng thời.

25. Tại sao WebSocket server vẫn có thể có race condition mặc dù mỗi TCP connection riêng biệt?

26. Làm thế nào để đảm bảo thread-safe connection manager?

### Authentication

27. JWT được validate ở đâu?
28. Authentication khác Authorization thế nào?
29. Có nên tin `userID` do client gửi lên không?
30. Vì sao authorization phải thực hiện ở server?

### Production

31. Một user có nhiều devices thì quản lý connection thế nào?
32. Dead connection được detect thế nào?
33. Slow client ảnh hưởng broadcast như thế nào?
34. Nếu một client không đọc message thì sao?
35. Server restart thì chuyện gì xảy ra?
36. Nếu có 10,000 WebSocket connections thì architecture hiện tại có vấn đề gì?