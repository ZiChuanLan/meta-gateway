import { Activity, KeyRound, Route as RouteIcon, Server } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { useSession } from '../session'
import { DataTable, Empty, ErrorState, Loading, Page, Panel, StatusBadge, formatDate } from '../components/ui'

export function Dashboard() {
  const { client } = useSession(); const service = api(client!)
  const sites = useQuery({ queryKey: ['sites'], queryFn: ({ signal }) => service.sites(signal) })
  const channels = useQuery({ queryKey: ['channels'], queryFn: ({ signal }) => service.channels(signal) })
  const routes = useQuery({ queryKey: ['routes'], queryFn: ({ signal }) => service.routes(signal) })
  const keys = useQuery({ queryKey: ['keys'], queryFn: ({ signal }) => service.keys(signal) })
  const proxy = useQuery({ queryKey: ['proxy-logs'], queryFn: ({ signal }) => service.proxyLogs(signal) })
  const checkins = useQuery({ queryKey: ['checkin-logs', 'dashboard'], queryFn: ({ signal }) => service.checkinLogs('?limit=5', signal) })
  const audit = useQuery({ queryKey: ['audit', undefined], queryFn: ({ signal }) => service.auditEvents(undefined, signal) })
  const core = [sites, channels, routes, keys]
  return <Page title="Dashboard" description="Gateway health, inventory, and recent operational activity.">
    <div className="stat-grid">
      <Stat icon={<Server/>} label="Sites" value={sites.data?.length}/><Stat icon={<Activity/>} label="Channels" value={channels.data?.length}/><Stat icon={<RouteIcon/>} label="Routes" value={routes.data?.length}/><Stat icon={<KeyRound/>} label="Downstream keys" value={keys.data?.length}/>
    </div>
    {core.some((q) => q.isPending) && <Loading/>}{core.some((q) => q.isError) && <ErrorState error={core.find((q) => q.error)?.error}/>} 
    <div className="dashboard-grid">
      <Panel title="Recent proxy attempts">{proxy.isPending ? <Loading/> : proxy.isError ? <ErrorState error={proxy.error}/> : <DataTable headers={['Model', 'Channel', 'Status', 'Latency', 'Time']} empty={!proxy.data?.length}>{proxy.data?.slice(0, 6).map((log) => <tr key={log.id}><td><strong>{log.model}</strong><small>Attempt {log.attempt}</small></td><td>#{log.channel_id}</td><td><StatusBadge value={String(log.status)}/></td><td>{log.latency_ms} ms</td><td>{formatDate(log.created_at)}</td></tr>)}</DataTable>}</Panel>
      <Panel title="Recent check-ins">{checkins.isPending ? <Loading/> : checkins.isError ? <ErrorState error={checkins.error}/> : !checkins.data?.length ? <Empty/> : <div className="activity-list">{checkins.data.map((log) => <div key={log.id}><StatusBadge value={log.status}/><div><strong>Credential #{log.credential_id}</strong><span>{log.category} · {formatDate(log.ran_at)}</span></div></div>)}</div>}</Panel>
      <Panel title="Recent admin activity" className="span-two">{audit.isPending ? <Loading/> : audit.isError ? <ErrorState error={audit.error}/> : <DataTable headers={['Action', 'Resource', 'Outcome', 'Status', 'Time']} empty={!audit.data?.length}>{audit.data?.slice(0, 8).map((event) => <tr key={event.id}><td><strong>{event.action}</strong></td><td>{event.resource_kind || '-'}{event.resource_id ? ` #${event.resource_id}` : ''}</td><td><StatusBadge value={event.outcome}/></td><td>{event.status_code}</td><td>{formatDate(event.created_at)}</td></tr>)}</DataTable>}</Panel>
    </div>
  </Page>
}
function Stat({ icon, label, value }: { icon: React.ReactNode; label: string; value?: number }) { return <div className="stat"><span>{icon}</span><div><small>{label}</small><strong>{value ?? '-'}</strong></div></div> }
