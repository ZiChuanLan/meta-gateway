import { Activity, Gauge, LogOut, Menu, Network, Route as RouteIcon, Server, Upload, X } from 'lucide-react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Navigate, NavLink, Route, Routes, useLocation } from 'react-router-dom'
import { useEffect, useState } from 'react'
import { ApiClient, ApiError, api } from './api/client'
import { useSession } from './session'
import { Button, ErrorState, Field, IconButton, Loading, StatusBadge } from './components/ui'
import { Dashboard } from './features/Dashboard'
import { Assets } from './features/Assets'
import { Routing } from './features/Routing'
import { Operations } from './features/Operations'
import { Exchange } from './features/Exchange'

export function App() {
  const { client, disconnect } = useSession()
  const queryClient = useQueryClient()
  useEffect(() => { if (!client) queryClient.clear() }, [client, queryClient])
  if (!client) return <Connect/>
  return <Authenticated clientKey={client} onUnauthorized={disconnect}/>
}

function Connect() {
  const { connect } = useSession()
  const [token, setToken] = useState('')
  const [remember, setRemember] = useState(true)
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  async function submit(e: React.FormEvent) {
    e.preventDefault(); if (!token.trim()) return
    setPending(true); setError('')
    try { await api(new ApiClient(token.trim())).sites(); connect(token, remember) }
    catch (err) { setError(err instanceof ApiError ? err.message : 'Connection failed') }
    finally { setPending(false) }
  }
  return <div className="connect-page"><section className="connect-panel"><div className="brand-mark"><Network size={24}/></div><h1>Meta Gateway</h1><p>Connect to the operational console.</p><form onSubmit={submit}><Field label="Admin token"><input autoFocus type="password" value={token} onChange={(e) => setToken(e.target.value)} autoComplete="current-password" required/></Field><label className="check"><input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)}/><span>Remember for this browser tab</span></label>{error && <div className="inline-error">{error}</div>}<Button type="submit" disabled={pending || !token.trim()}>{pending ? 'Connecting...' : 'Connect'}</Button></form><small>The token stays in memory or session storage and is never placed in a URL.</small></section></div>
}

function Authenticated({ clientKey, onUnauthorized }: { clientKey: object; onUnauthorized: () => void }) {
  const { client } = useSession(); const [open, setOpen] = useState(false); const location = useLocation()
  const ready = useQuery({ queryKey: ['ready'], queryFn: async () => { const response = await fetch('/readyz'); return response.ok }, refetchInterval: 30_000 })
  const auth = useQuery({ queryKey: ['auth', clientKey], queryFn: ({ signal }) => api(client!).sites(signal) })
  useEffect(() => { if (auth.error instanceof ApiError && auth.error.status === 401) onUnauthorized() }, [auth.error, onUnauthorized])
  useEffect(() => setOpen(false), [location.pathname])
  if (auth.isPending) return <div className="fullscreen-state"><Loading/></div>
  if (auth.isError) return <div className="fullscreen-state"><ErrorState error={auth.error} retry={() => auth.refetch()}/><Button variant="secondary" onClick={onUnauthorized}>Disconnect</Button></div>
  const nav = [{ to: '/', label: 'Dashboard', icon: Gauge }, { to: '/assets', label: 'Assets', icon: Server }, { to: '/routing', label: 'Routing', icon: RouteIcon }, { to: '/operations', label: 'Operations', icon: Activity }, { to: '/exchange', label: 'Exchange', icon: Upload }]
  return <div className="app-shell"><header className="mobile-header"><IconButton label="Open navigation" onClick={() => setOpen(true)}><Menu/></IconButton><strong>Meta Gateway</strong><StatusBadge value={ready.data ? 'ready' : 'unavailable'}/></header><aside className={open ? 'sidebar open' : 'sidebar'}><div className="sidebar-brand"><div className="brand-mark"><Network size={20}/></div><div><strong>Meta Gateway</strong><span>Admin Console</span></div><IconButton label="Close navigation" onClick={() => setOpen(false)}><X/></IconButton></div><nav>{nav.map(({ to, label, icon: Icon }) => <NavLink key={to} to={to} end={to === '/'}><Icon size={18}/><span>{label}</span></NavLink>)}</nav><div className="sidebar-footer"><div className="connection"><span className={ready.data ? 'dot healthy' : 'dot'}></span><div><strong>{ready.data ? 'Gateway ready' : 'Gateway unavailable'}</strong><span>Admin session active</span></div></div><button onClick={onUnauthorized}><LogOut size={17}/>Disconnect</button></div></aside>{open && <button className="drawer-scrim" aria-label="Close navigation" onClick={() => setOpen(false)}/>}<div className="content"><Routes><Route index element={<Dashboard/>}/><Route path="assets" element={<Assets/>}/><Route path="routing" element={<Routing/>}/><Route path="operations" element={<Operations/>}/><Route path="exchange" element={<Exchange/>}/><Route path="*" element={<Navigate to="/" replace/>}/></Routes></div></div>
}
