import { useCallback, useEffect, useRef, useState } from "react";
import {
  Bell,
  Check,
  CheckCheck,
  FileText,
  RefreshCw,
  ShieldAlert,
  X,
} from "lucide-react";
import { api, date, post } from "./api";
import { Badge, Empty } from "./components";
import { statusTone } from "./status";

type Notification = {
  id: string;
  kind: string;
  title: string;
  body: string;
  severity: string;
  readAt?: string;
  createdAt: string;
};

export default function NotificationCenter() {
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<Notification[]>([]);
  const [loading, setLoading] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await api<{ items: Notification[] }>(
        "/api/v1/me/notifications?limit=50",
      );
      setItems(response.items);
    } catch {
      // The rest of the application remains usable when notifications fail.
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const initial = window.setTimeout(() => void load(), 0);
    const refresh = window.setInterval(() => void load(), 60_000);
    return () => {
      window.clearTimeout(initial);
      window.clearInterval(refresh);
    };
  }, [load]);

  useEffect(() => {
    if (!open) return;
    const dismiss = (event: MouseEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", dismiss);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("mousedown", dismiss);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  async function read(notification: Notification) {
    if (notification.readAt) return;
    try {
      await post(`/api/v1/me/notifications/${notification.id}/read`, {});
      const readAt = new Date().toISOString();
      setItems((current) =>
        current.map((item) =>
          item.id === notification.id ? { ...item, readAt } : item,
        ),
      );
    } catch {
      await load();
    }
  }

  async function readAll() {
    try {
      await post("/api/v1/me/notifications/read-all", {});
      const readAt = new Date().toISOString();
      setItems((current) =>
        current.map((item) => ({ ...item, readAt: item.readAt || readAt })),
      );
    } catch {
      await load();
    }
  }

  const unread = items.filter((item) => !item.readAt).length;
  return (
    <div className="notification-area" ref={root}>
      <button
        type="button"
        className={`icon-button ${unread ? "has-dot" : ""}`}
        title="알림"
        aria-label={`알림${unread ? `, 읽지 않음 ${unread}개` : ""}`}
        aria-expanded={open}
        onClick={() => {
          const next = !open;
          setOpen(next);
          if (next) void load();
        }}
      >
        <Bell />
        {unread > 0 && <em>{unread > 9 ? "9+" : unread}</em>}
      </button>
      {open && (
        <section
          className="notification-popover"
          role="dialog"
          aria-label="알림 센터"
        >
          <header>
            <div>
              <h3>알림</h3>
              <Badge tone={unread ? "warning" : "neutral"}>
                {unread}개 읽지 않음
              </Badge>
            </div>
            <div className="notification-tools">
              <button
                type="button"
                className="icon-button"
                onClick={() => void readAll()}
                disabled={!unread}
                title="모두 읽음"
                aria-label="모든 알림 읽음 처리"
              >
                <CheckCheck />
              </button>
              <button
                type="button"
                className="icon-button"
                onClick={() => void load()}
                disabled={loading}
                title="새로고침"
                aria-label="알림 새로고침"
              >
                <RefreshCw className={loading ? "spin" : ""} />
              </button>
              <button
                type="button"
                className="icon-button"
                onClick={() => setOpen(false)}
                aria-label="알림 닫기"
              >
                <X />
              </button>
            </div>
          </header>
          <div className="notification-list" aria-live="polite">
            {items.length ? (
              items.map((notification) => (
                <button
                  type="button"
                  className={notification.readAt ? "read" : ""}
                  onClick={() => void read(notification)}
                  key={notification.id}
                  aria-label={`${notification.title}${notification.readAt ? ", 읽음" : ", 읽지 않음"}`}
                >
                  <span
                    className={`notification-icon ${statusTone(notification.severity)}`}
                  >
                    {notification.kind.includes("expiry") ? (
                      <FileText />
                    ) : (
                      <ShieldAlert />
                    )}
                  </span>
                  <span className="notification-copy">
                    <span>
                      <b>{notification.title}</b>
                      {!notification.readAt && <i />}
                    </span>
                    <p>{notification.body}</p>
                    <small>{date(notification.createdAt)}</small>
                  </span>
                  {notification.readAt && <Check className="notification-read" />}
                </button>
              ))
            ) : (
              <Empty
                icon={<Bell />}
                title={loading ? "알림을 확인하는 중입니다" : "새 알림이 없습니다"}
                description="계약, 문서, 평가와 Risk 알림이 여기에 표시됩니다."
              />
            )}
          </div>
        </section>
      )}
    </div>
  );
}
