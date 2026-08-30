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
  try { return localStorage.getItem(SESSION_KEY) ?? sessionStorage.getItem(SESSION_KEY) } catch { return null }
}

function storeToken(token: string | null, remember: boolean) {
  try {
    if (token && remember) localStorage.setItem(SESSION_KEY, token)
    else {
      localStorage.removeItem(SESSION_KEY)
      sessionStorage.removeItem(SESSION_KEY)
    }
  } catch {
    // Storage can be unavailable in hardened/private browser contexts.
  }
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(initialToken)
  const connect = useCallback((next: string, remember: boolean) => {
    const trimmed = next.trim()
    storeToken(trimmed, remember)
    setToken(trimmed)
  }, [])
  const disconnect = useCallback(() => {
    storeToken(null, false)
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
