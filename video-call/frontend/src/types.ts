export interface User {
  id: number
  username: string
  email: string
  display_name: string
  avatar_url: string
  created_at: string
  updated_at: string
}

export interface OnlineUser {
  id: number
  username: string
  display_name: string
  online: boolean
}

export interface AuthResponse {
  token: string
  user: User
}

export interface SignalMessage {
  type: string
  from: number
  to: number
  payload: unknown
  fromName: string
}

export type CallStatus = 'idle' | 'calling' | 'receiving' | 'connected' | 'ended'
