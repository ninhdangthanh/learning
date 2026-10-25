# 🎥 Video Call App

Real-time peer-to-peer video calling using **WebRTC (UDP)** for media, **WebSocket** for signaling, and **JWT** for auth.

## Stack

| Layer | Tech |
|-------|------|
| Frontend | React 18 + TypeScript + Vite |
| Backend | Go 1.21 + gorilla/websocket + sqlite3 |
| Media | WebRTC (DTLS-SRTP over UDP) |
| Auth | JWT (HS256) + bcrypt |
| Infra | Docker Compose + nginx |
| Tunnel | ngrok |

---

## Quick Start (Local Dev)

```bash
# 1. Install deps
make install

# 2. Run backend + frontend in parallel
make dev

# 3. Open http://localhost:5173
```

## Docker

```bash
# Build & start all containers (detached)
make up

# Frontend → http://localhost:3000
# Backend  → http://localhost:8080
```

## Docs

- [Hướng dẫn flow chạy (từ UI → API → WebRTC)](docs/HUONG_DAN_FLOW.md)
- [AWS deployment guide](docs/AWS_DEPLOYMENT_GUIDE.md)
- [Route 53, ACM, and ALB notes](docs/route53_acm_alb_notes.md)
- [WebRTC architecture notes](docs/WEBRTC_ARCHITECTURE_NOTES.md)
- TODO: [TURN coturn AWS setup](docs/TURN_COTURN_AWS_SETUP.md)
- [AWS ECS architecture questions](docs/aws_ecs_architecture_questions.md)
- [Deploy review for interviews](docs/interview_deploy_review.md)
- [Deploy steps for interviews](docs/interview_deploy_steps.md)
- [Original implementation plan](docs/plan.md)

## Testing on Local WiFi

To test video calls between two different devices (e.g., your laptop and your phone):

1.  **Ensure both devices are on the same WiFi.**
2.  **Find your local IP address**:
    <details>
    <summary><b>macOS</b></summary>

    - **Terminal**: Run `ipconfig getifaddr en0` (or `en1` if on older MacBook/Ethernet).
    - **GUI**: `System Settings` > `Network` > Select `Wi-Fi` > Click `Details...` > Look for `IP address`.
    </details>

    <details>
    <summary><b>Windows</b></summary>

    - **CMD**: Run `ipconfig` and look for `IPv4 Address` under your active adapter.
    - **GUI**: `Settings` > `Network & internet` > `Wi-Fi` > `Properties` > Look for `IPv4 address`.
    </details>
3.  **Start the app**:
    ```bash
    make dev
    ```
4.  **Access from other devices**:
    *   On your phone/other device, open the browser and go to `http://<YOUR_IP>:5173`.
    *   Example: `http://192.168.1.13:5173`

> [!NOTE]
> The app is configured to automatically detect the IP address from the URL, so it will connect to the backend running on your computer correctly.

## API

| Method | Route | Auth | Description |
|--------|-------|------|-------------|
| POST | `/api/auth/register` | — | Create account |
| POST | `/api/auth/login` | — | Sign in |
| GET | `/api/auth/me` | Bearer | Current user |
| GET | `/api/users` | Bearer | List users |
| WS | `/ws?token=<jwt>` | JWT query | Signaling channel |

## How the call works

```
Alice ──call──▶ WebSocket Hub ──▶ Bob
Alice ◀──offer── (SDP via WS)
Bob  ──answer──▶ WS Hub ──▶ Alice
Both exchange ICE candidates (UDP ports)
       ┌─────────────────┐
       │  WebRTC P2P     │
       │  (UDP / SRTP)   │
       └─────────────────┘
```
