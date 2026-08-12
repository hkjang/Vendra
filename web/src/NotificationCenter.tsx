import { useEffect, useState } from "react";
import { Bell, Check, FileText, ShieldAlert, X } from "lucide-react";
import { api, date, post } from "./api";
import { Badge, Empty, statusTone } from "./components";

type Notification = { id:string; kind:string; title:string; body:string; severity:string; readAt?:string; createdAt:string };

export default function NotificationCenter() {
  const [open,setOpen]=useState(false);
  const [items,setItems]=useState<Notification[]>([]);
  const load=()=>api<{items:Notification[]}>('/api/v1/me/notifications?limit=50').then(x=>setItems(x.items)).catch(()=>{});
  useEffect(()=>{load()},[]);
  async function read(id:string){await post(`/api/v1/me/notifications/${id}/read`,{});load()}
  const unread=items.filter(x=>!x.readAt).length;
  return <div className="notification-area"><button className={`icon-button ${unread?'has-dot':''}`} title="알림" onClick={()=>setOpen(v=>!v)}><Bell/>{unread>0&&<em>{unread>9?'9+':unread}</em>}</button>{open&&<div className="notification-popover"><header><div><h3>알림</h3><Badge tone={unread?'warning':'neutral'}>{unread}개 읽지 않음</Badge></div><button className="icon-button" onClick={()=>setOpen(false)}><X/></button></header><div className="notification-list">{items.length?items.map(n=><button className={n.readAt?'read':''} onClick={()=>read(n.id)} key={n.id}><span className={`notification-icon ${statusTone(n.severity)}`}>{n.kind.includes('expiry')?<FileText/>:<ShieldAlert/>}</span><div><span><b>{n.title}</b>{!n.readAt&&<i/>}</span><p>{n.body}</p><small>{date(n.createdAt)}</small></div>{n.readAt&&<Check/>}</button>):<Empty icon={<Bell/>} title="새 알림이 없습니다" description="계약, 문서, 평가와 Risk 알림이 여기에 표시됩니다."/>}</div></div>}</div>;
}
