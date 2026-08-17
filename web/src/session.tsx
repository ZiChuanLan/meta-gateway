import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react'
import { ApiClient } from './api/client'

const SESSION_KEY = 'meta-gateway.admin-token'

interface SessionValue {
  token: string | null
  client: ApiClient | null
  connect: (token: string, remember: boolean) => void
  disconnect: () => void
}

const SessionContext = createContext<SessionValue | null>(null)

function initialToken() {
  try { return sessionStorage.getItem(SESSION_KEY) } catch { return null }
}

function storeToken(token: string | null) {
  try {
    if (token) sessionStorage.setItem(SESSION_KEY, token)
    else sessionStorage.removeItem(SESSION_KEY)
  } catch {
    // Storage can be unavailable in hardened/private browser contexts.
  }
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(initialToken)
  const connect = useCallback((next: string, remember: boolean) => {
    const trimmed = next.trim()
    storeToken(remember ? trimmed : null)
    setToken(trimmed)
  }, [])
  const disconnect = useCallback(() => {
    storeToken(null)
    setToken(null)
  }, [])
  const value = useMemo(() => ({ token, client: token ? new ApiClient(token, disconnect) : null, connect, disconnect }), [token, connect, disconnect])
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}

export function useSession() {
  const value = useContext(SessionContext)
  if (!value) throw new Error('useSession must be used inside SessionProvider')
  return value
}
