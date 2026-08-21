import { Component, ReactNode, useEffect, useId, useRef } from 'react'
import { AlertTriangle, CheckCircle2, Info, LoaderCircle, RefreshCw, X, XCircle } from 'lucide-react'
import { statusTone } from './status'

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

export function Modal({ title, description, children, onClose, wide=false }: { title:string;description?:string;children:ReactNode;onClose:()=>void;wide?:boolean }) {
  const ref=useRef<HTMLDivElement>(null)
  const titleId=useId()
  useEffect(()=>{const previous=document.activeElement as HTMLElement|null;const overflow=document.body.style.overflow;const f=(e:KeyboardEvent)=>{if(e.key==='Escape')onClose()};document.addEventListener('keydown',f);document.body.style.overflow='hidden';ref.current?.focus();return()=>{document.removeEventListener('keydown',f);document.body.style.overflow=overflow;previous?.focus()}},[onClose])
  return <div className="modal-backdrop" role="presentation" onMouseDown={e=>{if(e.target===e.currentTarget)onClose()}}><div className={`modal ${wide?'wide':''}`} role="dialog" aria-modal="true" aria-labelledby={titleId} tabIndex={-1} ref={ref}><header><div><h2 id={titleId}>{title}</h2>{description&&<p>{description}</p>}</div><button type="button" className="icon-button" onClick={onClose} aria-label="닫기"><X/></button></header><div className="modal-body">{children}</div></div></div>
}

export function Toast({ type='success', message, onClose }: { type?:'success'|'error'|'info';message:string;onClose:()=>void }) {
  useEffect(()=>{const t=setTimeout(onClose,4500);return()=>clearTimeout(t)},[onClose])
  return <div className={`toast ${type}`} role={type==='error'?'alert':'status'}>{type==='success'?<CheckCircle2/>:type==='error'?<XCircle/>:<Info/>}<span>{message}</span><button type="button" onClick={onClose} aria-label="알림 닫기"><X/></button></div>
}

export function RiskBadge({ level }: { level?:string }) { return <Badge tone={statusTone(level)}>{level || '미평가'}</Badge> }

export function ScoreRing({ score, size='normal' }: { score?:number;size?:'small'|'normal' }) {
  const v=Math.max(0,Math.min(100,score||0));return <div className={`score-ring ${size}`} style={{'--score':`${v*3.6}deg`} as React.CSSProperties}><div><strong>{score==null?'—':Math.round(score)}</strong>{size==='normal'&&<span>/ 100</span>}</div></div>
}

export function Field({ label, children, hint, required=false }: { label:string;children:ReactNode;hint?:string;required?:boolean }) { return <label className="field"><span>{label}{required&&<em>*</em>}</span>{children}{hint&&<small>{hint}</small>}</label> }

export function Confirm({ title, body, confirmLabel='확인', danger=false, onConfirm, onClose }: { title:string;body:string;confirmLabel?:string;danger?:boolean;onConfirm:()=>void;onClose:()=>void }) {
  return <Modal title={title} onClose={onClose}><div className="confirm-body"><AlertTriangle/><p>{body}</p></div><div className="form-actions"><button type="button" className="button secondary" onClick={onClose}>취소</button><button type="button" className={`button ${danger?'danger':''}`} onClick={onConfirm}>{confirmLabel}</button></div></Modal>
}

// A page chunk can fail to load when the server has been upgraded while a tab
// stayed open: the cached index.html points at a hash that no longer exists.
// Without this boundary React unmounts the whole tree and the user sees a blank
// screen with no way forward.
export class PageErrorBoundary extends Component<{ children:ReactNode }, { failed:boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() { return { failed: true } }
  componentDidCatch(error: Error) { console.error('page failed to render', error) }
  render() {
    if (!this.state.failed) return this.props.children
    return <div className="empty page-error" role="alert"><div className="empty-icon"><AlertTriangle/></div><h3>화면을 불러오지 못했습니다</h3><p>서비스가 업데이트되었을 수 있습니다. 새로고침하면 최신 화면을 불러옵니다.</p><button type="button" className="button" onClick={()=>window.location.reload()}><RefreshCw/>새로고침</button></div>
  }
}
