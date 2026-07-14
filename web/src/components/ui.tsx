import { AlertTriangle, LoaderCircle, X } from 'lucide-react'
import { useEffect, type ReactNode } from 'react'
import { ApiError } from '../api/client'

export function Button({ children, variant = 'primary', icon, ...props }: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'secondary' | 'danger' | 'quiet'; icon?: ReactNode }) {
  return <button className={`button button-${variant}`} {...props}>{icon}{children}</button>
}

export function IconButton({ label, children, ...props }: React.ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return <button className="icon-button" aria-label={label} title={label} {...props}>{children}</button>
}

export function Page({ title, description, actions, children }: { title: string; description: string; actions?: ReactNode; children: ReactNode }) {
  return <main className="page"><header className="page-header"><div><h1>{title}</h1><p>{description}</p></div>{actions && <div className="toolbar">{actions}</div>}</header>{children}</main>
}

export function Panel({ title, actions, children, className = '' }: { title?: string; actions?: ReactNode; children: ReactNode; className?: string }) {
  return <section className={`panel ${className}`}>{(title || actions) && <header className="panel-header">{title && <h2>{title}</h2>}<div className="toolbar">{actions}</div></header>}{children}</section>
}

export function Dialog({ title, children, onClose, actions, danger = false }: { title: string; children: ReactNode; onClose: () => void; actions?: ReactNode; danger?: boolean }) {
  useEffect(() => { const close = (e: KeyboardEvent) => e.key === 'Escape' && onClose(); window.addEventListener('keydown', close); return () => window.removeEventListener('keydown', close) }, [onClose])
  return <div className="dialog-backdrop" role="presentation" onMouseDown={(e) => e.target === e.currentTarget && onClose()}><section className="dialog" role="dialog" aria-modal="true" aria-labelledby="dialog-title"><header><div className={danger ? 'danger-title' : ''}>{danger && <AlertTriangle size={18}/>}<h2 id="dialog-title">{title}</h2></div><IconButton label="Close" onClick={onClose}><X size={18}/></IconButton></header><div className="dialog-body">{children}</div>{actions && <footer>{actions}</footer>}</section></div>
}

export function ConfirmDialog({ title, message, confirmLabel = 'Delete', pending, onConfirm, onClose }: { title: string; message: string; confirmLabel?: string; pending?: boolean; onConfirm: () => void; onClose: () => void }) {
  return <Dialog title={title} onClose={onClose} danger actions={<><Button variant="secondary" onClick={onClose}>Cancel</Button><Button variant="danger" disabled={pending} onClick={onConfirm}>{pending ? 'Working...' : confirmLabel}</Button></>}><p>{message}</p></Dialog>
}

export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) { return <label className="field"><span>{label}</span>{children}{hint && <small>{hint}</small>}</label> }
export function StatusBadge({ value }: { value: string | boolean }) { const text = typeof value === 'boolean' ? (value ? 'Enabled' : 'Disabled') : value; return <span className={`badge badge-${String(text).toLowerCase().replaceAll('_', '-')}`}>{text}</span> }
export function Loading() { return <div className="state"><LoaderCircle className="spin" size={22}/><span>Loading</span></div> }
export function Empty({ children = 'Nothing here yet.' }: { children?: ReactNode }) { return <div className="state state-empty">{children}</div> }
export function ErrorState({ error, retry }: { error: unknown; retry?: () => void }) { const text = error instanceof ApiError ? error.message : 'Something went wrong'; return <div className="state state-error"><AlertTriangle size={20}/><span>{text}</span>{retry && <Button variant="secondary" onClick={retry}>Retry</Button>}</div> }
export function DataTable({ headers, children, empty }: { headers: string[]; children: ReactNode; empty?: boolean }) { if (empty) return <Empty/>; return <div className="table-wrap"><table><thead><tr>{headers.map((h) => <th key={h}>{h}</th>)}</tr></thead><tbody>{children}</tbody></table></div> }
export function Tabs({ items, active, onChange }: { items: string[]; active: string; onChange: (value: string) => void }) { return <div className="tabs" role="tablist">{items.map((item) => <button role="tab" aria-selected={active === item} key={item} onClick={() => onChange(item)}>{item}</button>)}</div> }
export function formatDate(value?: string) { if (!value) return '-'; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date) }
export function formatBytes(value: number) { if (value < 1024) return `${value} B`; if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`; return `${(value / 1024 ** 2).toFixed(1)} MB` }
