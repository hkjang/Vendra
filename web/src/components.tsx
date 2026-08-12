import { ReactNode, useEffect, useRef } from 'react'
import { AlertTriangle, CheckCircle2, Info, LoaderCircle, X, XCircle } from 'lucide-react'

export function Logo({ compact = false }: { compact?: boolean }) {
  return <div className="logo-wrap"><div className="logo-mark"><i/><i/><i/></div>{!compact && <div><strong>Vendra</strong><span>Supplier intelligence</span></div>}</div>
}

export function PageHeader({ eyebrow, title, description, actions }: { eyebrow?:string; title:string; description?:string; actions?:ReactNode }) {
  return <header className="page-header"><div>{eyebrow && <p className="eyebrow">{eyebrow}</p>}<h1>{title}</h1>{description && <p>{description}</p>}</div>{actions && <div className="header-actions">{actions}</div>}</header>
}

export function Empty({ icon, title, description, action }: { icon?:ReactNode; title:string; description:string; action?:ReactNode }) {
  return <div className="empty"><div className="empty-icon">{icon || <Info/>}</div><h3>{title}</h3><p>{description}</p>{action}</div>
}

export function Loading({ label='불러오는 중' }: { label?:string }) { return <div className="loading"><LoaderCircle className="spin"/><span>{label}</span></div> }

export function Badge({ children, tone='neutral' }: { children:ReactNode; tone?:'neutral'|'success'|'warning'|'danger'|'info'|'purple' }) { return <span className={`badge ${tone}`}>{children}</span> }

export function statusTone(status?: string): 'neutral'|'success'|'warning'|'danger'|'info'|'purple' {
  const s=(status||'').toLowerCase(); if(['active','approved','completed','pass','low','s','a'].includes(s))return 'success'; if(['high','critical','rejected','suspended','failed'].includes(s))return 'danger'; if(['pending','screening','registration','improvement','medium','conditional_pass'].includes(s))return 'warning'; if(['draft','candidate'].includes(s))return 'neutral'; return 'info'
}

export function Modal({ title, description, children, onClose, wide=false }: { title:string;description?:string;children:ReactNode;onClose:()=>void;wide?:boolean }) {
  const ref=useRef<HTMLDivElement>(null)
  useEffect(()=>{const f=(e:KeyboardEvent)=>{if(e.key==='Escape')onClose()};document.addEventListener('keydown',f);ref.current?.focus();return()=>document.removeEventListener('keydown',f)},[onClose])
  return <div className="modal-backdrop" role="presentation" onMouseDown={e=>{if(e.target===e.currentTarget)onClose()}}><div className={`modal ${wide?'wide':''}`} role="dialog" aria-modal="true" tabIndex={-1} ref={ref}><header><div><h2>{title}</h2>{description&&<p>{description}</p>}</div><button className="icon-button" onClick={onClose} aria-label="닫기"><X/></button></header><div className="modal-body">{children}</div></div></div>
}

export function Toast({ type='success', message, onClose }: { type?:'success'|'error'|'info';message:string;onClose:()=>void }) {
  useEffect(()=>{const t=setTimeout(onClose,4500);return()=>clearTimeout(t)},[onClose])
  return <div className={`toast ${type}`}>{type==='success'?<CheckCircle2/>:type==='error'?<XCircle/>:<Info/>}<span>{message}</span><button onClick={onClose}><X/></button></div>
}

export function RiskBadge({ level }: { level?:string }) { return <Badge tone={statusTone(level)}>{level || '미평가'}</Badge> }

export function ScoreRing({ score, size='normal' }: { score?:number;size?:'small'|'normal' }) {
  const v=Math.max(0,Math.min(100,score||0));return <div className={`score-ring ${size}`} style={{'--score':`${v*3.6}deg`} as React.CSSProperties}><div><strong>{score==null?'—':Math.round(score)}</strong>{size==='normal'&&<span>/ 100</span>}</div></div>
}

export function Field({ label, children, hint, required=false }: { label:string;children:ReactNode;hint?:string;required?:boolean }) { return <label className="field"><span>{label}{required&&<em>*</em>}</span>{children}{hint&&<small>{hint}</small>}</label> }

export function Confirm({ title, body, confirmLabel='확인', danger=false, onConfirm, onClose }: { title:string;body:string;confirmLabel?:string;danger?:boolean;onConfirm:()=>void;onClose:()=>void }) {
  return <Modal title={title} onClose={onClose}><div className="confirm-body"><AlertTriangle/><p>{body}</p></div><div className="form-actions"><button className="button secondary" onClick={onClose}>취소</button><button className={`button ${danger?'danger':''}`} onClick={onConfirm}>{confirmLabel}</button></div></Modal>
}
