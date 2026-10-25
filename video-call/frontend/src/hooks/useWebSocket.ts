import { useEffect, useRef, useCallback, useState } from 'react'
import { WS_URL } from '../config'
import { SignalMessage, OnlineUser } from '../types'

interface UseWebSocketReturn {
  sendMessage: (msg: SignalMessage) => void
  onlineUsers: OnlineUser[]
  signalMessages: SignalMessage[]
  connected: boolean
}

export function useWebSocket(token: string | null): UseWebSocketReturn {
  const wsRef = useRef<WebSocket | null>(null)
  const [onlineUsers, setOnlineUsers] = useState<OnlineUser[]>([])
  const [signalMessages, setSignalMessages] = useState<SignalMessage[]>([])
  const [connected, setConnected] = useState(false)
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout>>()

  const connect = useCallback(() => {
    if (!token) return

    const ws = new WebSocket(`${WS_URL}?token=${token}`)
    wsRef.current = ws

    ws.onopen = () => {
      console.log('✅ WebSocket connected')
      setConnected(true)
    }

    ws.onmessage = (event) => {
      try {
        const msg: SignalMessage = JSON.parse(event.data)

        if (msg.type === 'users-list') {
          setOnlineUsers(msg.payload as OnlineUser[])
        } else {
          setSignalMessages((messages) => [...messages, msg])
        }
      } catch (err) {
        console.error('Failed to parse message:', err)
      }
    }

    ws.onclose = () => {
      console.log('❌ WebSocket disconnected')
      setConnected(false)
      // Reconnect after 3 seconds
      reconnectTimerRef.current = setTimeout(() => {
        connect()
      }, 3000)
    }

    ws.onerror = (err) => {
      console.error('WebSocket error:', err)
      ws.close()
    }
  }, [token])

  useEffect(() => {
    connect()

    return () => {
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current)
      }
      if (wsRef.current) {
        wsRef.current.close()
      }
    }
  }, [connect])

  const sendMessage = useCallback((msg: SignalMessage) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(msg))
    }
  }, [])

  return { sendMessage, onlineUsers, signalMessages, connected }
}
