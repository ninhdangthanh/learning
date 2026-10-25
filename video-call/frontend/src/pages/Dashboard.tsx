import { useAuth } from '../context/useAuth'
import { useWebSocket } from '../hooks/useWebSocket'
import { useWebRTC } from '../hooks/useWebRTC'
import VideoCall from '../components/VideoCall'
import IncomingCall from '../components/IncomingCall'
import UserList from '../components/UserList'

function Dashboard() {
  const { user, token, logout } = useAuth()
  const { sendMessage, onlineUsers, signalMessages, connected } = useWebSocket(token)
  const {
    localStream,
    remoteStream,
    callStatus,
    incomingCall,
    startCall,
    acceptCall,
    rejectCall,
    endCall,
    toggleAudio,
    toggleVideo,
    audioEnabled,
    videoEnabled,
  } = useWebRTC(user!.id, sendMessage, signalMessages)

  const otherUsers = onlineUsers.filter((u) => u.id !== user!.id)

  const dashboardClassName = `dashboard ${callStatus !== 'idle' ? 'dashboard--in-call' : ''}`

  return (
    <div className={dashboardClassName}>
      {/* Sidebar */}
      <aside className="sidebar">
        <div className="sidebar-header">
          <div className="sidebar-logo">
            <svg width="32" height="32" viewBox="0 0 48 48" fill="none">
              <rect width="48" height="48" rx="12" fill="url(#dash-logo)" />
              <path d="M16 20C16 17.7909 17.7909 16 20 16H28C30.2091 16 32 17.7909 32 20V28C32 30.2091 30.2091 32 28 32H20C17.7909 32 16 30.2091 16 28V20Z" stroke="white" strokeWidth="2" />
              <circle cx="22" cy="23" r="2" fill="white" />
              <circle cx="28" cy="23" r="2" fill="white" />
              <defs>
                <linearGradient id="dash-logo" x1="0" y1="0" x2="48" y2="48" gradientUnits="userSpaceOnUse">
                  <stop stopColor="#6366f1" />
                  <stop offset="1" stopColor="#8b5cf6" />
                </linearGradient>
              </defs>
            </svg>
            <span>VideoCall</span>
          </div>
        </div>

        <div className="sidebar-user">
          <div className="user-avatar">{user!.display_name.charAt(0).toUpperCase()}</div>
          <div className="user-info">
            <h3>{user!.display_name}</h3>
            <span className={`status-badge ${connected ? 'online' : 'offline'}`}>
              <span className="status-dot" />
              {connected ? 'Online' : 'Connecting...'}
            </span>
          </div>
        </div>

        <div className="sidebar-section">
          <h4>
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path d="M8 8C9.65685 8 11 6.65685 11 5C11 3.34315 9.65685 2 8 2C6.34315 2 5 3.34315 5 5C5 6.65685 6.34315 8 8 8Z" stroke="currentColor" strokeWidth="1.5" />
              <path d="M14 14C14 11.2386 11.3137 9 8 9C4.68629 9 2 11.2386 2 14" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
            </svg>
            Online Users ({otherUsers.length})
          </h4>
        </div>

        <UserList
          users={otherUsers}
          onCallUser={startCall}
          callStatus={callStatus}
          currentUserId={user!.id}
        />

        <div className="sidebar-bottom">
          <button className="logout-btn" onClick={logout}>
            <svg width="18" height="18" viewBox="0 0 18 18" fill="none">
              <path d="M6.75 15.75H3.75C3.35218 15.75 2.97064 15.592 2.68934 15.3107C2.40804 15.0294 2.25 14.6478 2.25 14.25V3.75C2.25 3.35218 2.40804 2.97064 2.68934 2.68934C2.97064 2.40804 3.35218 2.25 3.75 2.25H6.75" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
              <path d="M12 12.75L15.75 9L12 5.25" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
              <path d="M15.75 9H6.75" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            Sign Out
          </button>
        </div>
      </aside>

      {/* Main Content */}
      <main className="main-content">
        {callStatus === 'idle' ? (
          <div className="welcome-screen">
            <div className="welcome-icon">
              <svg width="80" height="80" viewBox="0 0 80 80" fill="none">
                <rect width="80" height="80" rx="20" fill="url(#welcome-grad)" fillOpacity="0.1" />
                <path d="M26 32C26 28.6863 28.6863 26 32 26H48C51.3137 26 54 28.6863 54 32V48C54 51.3137 51.3137 54 48 54H32C28.6863 54 26 51.3137 26 48V32Z" stroke="url(#welcome-grad)" strokeWidth="2.5" />
                <circle cx="36" cy="38" r="3" fill="url(#welcome-grad)" />
                <circle cx="46" cy="38" r="3" fill="url(#welcome-grad)" />
                <path d="M34 46C34 46 36 49 41 49C46 49 48 46 48 46" stroke="url(#welcome-grad)" strokeWidth="2" strokeLinecap="round" />
                <defs>
                  <linearGradient id="welcome-grad" x1="0" y1="0" x2="80" y2="80" gradientUnits="userSpaceOnUse">
                    <stop stopColor="#6366f1" />
                    <stop offset="1" stopColor="#8b5cf6" />
                  </linearGradient>
                </defs>
              </svg>
            </div>
            <h2>Ready to Connect</h2>
            <p>Select an online user from the sidebar to start a video call</p>
            <div className="welcome-features">
              <div className="feature-item">
                <div className="feature-icon">
                  <svg width="24" height="24" viewBox="0 0 24 24" fill="none">
                    <path d="M15.75 10.5L20.47 7.32C20.7 7.17 21 7.33 21 7.6V16.4C21 16.67 20.7 16.83 20.47 16.68L15.75 13.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                    <rect x="3" y="6" width="13" height="12" rx="2" stroke="currentColor" strokeWidth="1.5" />
                  </svg>
                </div>
                <div>
                  <h4>HD Video</h4>
                  <p>Crystal clear 720p video quality</p>
                </div>
              </div>
              <div className="feature-item">
                <div className="feature-icon">
                  <svg width="24" height="24" viewBox="0 0 24 24" fill="none">
                    <path d="M12 2L12 22" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
                    <path d="M8 5L8 19" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
                    <path d="M16 7L16 17" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
                    <path d="M4 9L4 15" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
                    <path d="M20 8L20 16" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
                  </svg>
                </div>
                <div>
                  <h4>Low Latency</h4>
                  <p>UDP-based transport for real-time communication</p>
                </div>
              </div>
              <div className="feature-item">
                <div className="feature-icon">
                  <svg width="24" height="24" viewBox="0 0 24 24" fill="none">
                    <path d="M12 22C12 22 20 18 20 12V5L12 2L4 5V12C4 18 12 22 12 22Z" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                  </svg>
                </div>
                <div>
                  <h4>Encrypted</h4>
                  <p>End-to-end encrypted with DTLS-SRTP</p>
                </div>
              </div>
            </div>
          </div>
        ) : (
          <VideoCall
            localStream={localStream}
            remoteStream={remoteStream}
            callStatus={callStatus}
            endCall={endCall}
            toggleAudio={toggleAudio}
            toggleVideo={toggleVideo}
            audioEnabled={audioEnabled}
            videoEnabled={videoEnabled}
          />
        )}
      </main>

      {/* Incoming Call Modal */}
      {incomingCall && callStatus === 'receiving' && (
        <IncomingCall
          callerName={incomingCall.fromName}
          onAccept={acceptCall}
          onReject={rejectCall}
        />
      )}
    </div>
  )
}

export default Dashboard
