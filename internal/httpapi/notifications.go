package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type notificationAdapter struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	URL            string `json:"url"`
	Enabled        bool   `json:"enabled"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

func (a *App) runBackground(ctx context.Context) {
	a.runBackgroundOnce(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.runBackgroundOnce(ctx)
		}
	}
}

// backgroundPassTimeout keeps one pass well inside the tick interval. Without
// it a single stuck query or integration would end the loop for good, silently
// stopping notifications and the retention sweep until the process restarts.
const backgroundPassTimeout = 30 * time.Minute

func (a *App) runBackgroundOnce(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, backgroundPassTimeout)
	defer cancel()
	if err := a.scheduleNotifications(ctx); err != nil {
		slog.Error("notification scheduling failed", "error", err)
	}
	if err := a.dispatchNotifications(ctx); err != nil {
		slog.Error("notification dispatch failed", "error", err)
	}
	if err := a.purgeExpired(ctx); err != nil {
		slog.Error("retention sweep failed", "error", err)
	}
}

func (a *App) scheduleNotifications(ctx context.Context) error {
	_, err := a.db.Exec(ctx, `INSERT INTO notifications(user_id,supplier_id,kind,title,body,severity,object_type,object_id)
	 SELECT o.owner_id,o.supplier_id,'contract_expiry',
	   CASE WHEN o.end_date<=current_date+7 THEN '계약 종료 7일 이내' WHEN o.end_date<=current_date+30 THEN '계약 종료 30일 이내' WHEN o.end_date<=current_date+90 THEN '계약 종료 90일 이내' ELSE '계약 종료 180일 이내' END,
	   o.title||' ('||o.number||') 계약이 '||to_char(o.end_date,'YYYY-MM-DD')||' 종료됩니다.',
	   CASE WHEN o.end_date<=current_date+30 THEN 'warning' ELSE 'info' END,'contract',o.id
	 FROM business_objects o WHERE o.object_type='contract' AND o.deleted_at IS NULL AND o.owner_id IS NOT NULL AND o.status NOT IN('ended','terminated') AND o.end_date BETWEEN current_date AND current_date+180
	 ON CONFLICT(user_id,kind,object_type,object_id,title) DO NOTHING`)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx, `INSERT INTO notifications(user_id,supplier_id,kind,title,body,severity,object_type,object_id)
	 SELECT d.uploaded_by,d.supplier_id,'document_expiry',CASE WHEN d.expires_at<=current_date+7 THEN '문서 만료 7일 이내' ELSE '문서 만료 30일 이내' END,
	 d.name||' 문서가 '||to_char(d.expires_at,'YYYY-MM-DD')||' 만료됩니다.','warning','document',d.id
	 FROM documents d WHERE d.status IN('active','approved') AND d.uploaded_by IS NOT NULL AND d.expires_at BETWEEN current_date AND current_date+30
	 ON CONFLICT(user_id,kind,object_type,object_id,title) DO NOTHING`)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx, `INSERT INTO notifications(user_id,supplier_id,kind,title,body,severity,object_type,object_id)
	 SELECT o.owner_id,o.supplier_id,'sla_breach','SLA 위반 즉시 조치',o.title||' ('||o.number||') SLA 위반이 등록되었습니다.','critical','issue',o.id
	 FROM business_objects o WHERE o.object_type='issue' AND o.deleted_at IS NULL AND o.owner_id IS NOT NULL AND o.status NOT IN('closed','resolved') AND lower(COALESCE(o.data->>'issueType',o.data->>'type','')) IN ('sla 위반','sla_violation','sla breach')
	 ON CONFLICT(user_id,kind,object_type,object_id,title) DO NOTHING`)
	if err != nil {
		return err
	}
	// Count only orders for the contract's own supplier. Joining on parent id
	// alone let an order placed with a different supplier inflate the total, and
	// parent_id is caller-supplied, so a critical overrun alert could be raised
	// on a contract the sender has nothing to do with.
	_, err = a.db.Exec(ctx, `INSERT INTO notifications(user_id,supplier_id,kind,title,body,severity,object_type,object_id)
	 SELECT c.owner_id,c.supplier_id,'contract_amount_exceeded','계약금액 초과',c.title||' 계약금액 '||COALESCE(c.amount,0)||' 대비 발주 누계 '||sum(po.amount)||' 입니다.','critical','contract',c.id
	 FROM business_objects c JOIN business_objects po ON po.parent_id=c.id AND po.object_type='purchase_order' AND po.deleted_at IS NULL AND po.supplier_id IS NOT DISTINCT FROM c.supplier_id
	 WHERE c.object_type='contract' AND c.deleted_at IS NULL AND c.owner_id IS NOT NULL AND c.amount IS NOT NULL
	 GROUP BY c.id HAVING sum(COALESCE(po.amount,0))>c.amount
	 ON CONFLICT(user_id,kind,object_type,object_id,title) DO NOTHING`)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx, `INSERT INTO notifications(user_id,supplier_id,kind,title,body,severity,object_type,object_id)
	 SELECT s.owner_id,s.id,'evaluation_due','공급업체 평가 예정',s.name||' 공급업체 평가기간이 시작됩니다.','info','supplier',s.id
	 FROM suppliers s WHERE s.deleted_at IS NULL AND s.owner_id IS NOT NULL AND CASE WHEN COALESCE(s.metadata->>'nextEvaluationDate','') ~ '^\d{4}-\d{2}-\d{2}$' THEN (s.metadata->>'nextEvaluationDate')::date END BETWEEN current_date AND current_date+30
	 ON CONFLICT(user_id,kind,object_type,object_id,title) DO NOTHING`)
	if err != nil {
		return err
	}
	var value []byte
	if err = a.db.QueryRow(ctx, `SELECT value FROM settings WHERE key='notification.adapters'`).Scan(&value); err != nil {
		return err
	}
	var adapters []notificationAdapter
	if json.Unmarshal(value, &adapters) != nil {
		return nil
	}
	for _, adapter := range adapters {
		if !adapter.Enabled || adapter.Name == "" {
			continue
		}
		_, err = a.db.Exec(ctx, `INSERT INTO notification_deliveries(notification_id,adapter) SELECT n.id,$1 FROM notifications n WHERE NOT EXISTS(SELECT 1 FROM notification_deliveries d WHERE d.notification_id=n.id AND d.adapter=$1)`, adapter.Name)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *App) dispatchNotifications(ctx context.Context) error {
	var value []byte
	if err := a.db.QueryRow(ctx, `SELECT value FROM settings WHERE key='notification.adapters'`).Scan(&value); err != nil {
		return err
	}
	var adapters []notificationAdapter
	if json.Unmarshal(value, &adapters) != nil {
		return nil
	}
	byName := map[string]notificationAdapter{}
	for _, adapter := range adapters {
		byName[adapter.Name] = adapter
	}
	rows, err := a.db.Query(ctx, `SELECT d.id,d.adapter,n.title,n.body,n.severity FROM notification_deliveries d JOIN notifications n ON n.id=d.notification_id WHERE d.status='pending' AND d.attempts<5 ORDER BY d.created_at LIMIT 50`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct{ id, adapter, title, body, severity string }
	items := []item{}
	for rows.Next() {
		var x item
		if err := rows.Scan(&x.id, &x.adapter, &x.title, &x.body, &x.severity); err != nil {
			return err
		}
		items = append(items, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, x := range items {
		adapter, ok := byName[x.adapter]
		if !ok || !adapter.Enabled {
			continue
		}
		deliveryErr := deliverNotification(ctx, adapter, x.title, x.body, x.severity)
		if deliveryErr != nil {
			if _, err := a.db.Exec(ctx, `UPDATE notification_deliveries SET attempts=attempts+1,response=$2 WHERE id=$1`, x.id, deliveryErr.Error()); err != nil {
				logDB(err)
			}
		} else {
			if _, err := a.db.Exec(ctx, `UPDATE notification_deliveries SET status='delivered',attempts=attempts+1,delivered_at=now(),response='ok' WHERE id=$1`, x.id); err != nil {
				logDB(err)
			}
		}
	}
	return nil
}

func deliverNotification(ctx context.Context, adapter notificationAdapter, title, body, severity string) error {
	switch adapter.Type {
	case "log":
		slog.Info("notification", "adapter", adapter.Name, "title", title, "body", body, "severity", severity)
		return nil
	case "slack", "mattermost", "webhook", "email", "sms", "internal_messenger":
		if adapter.URL == "" {
			return fmt.Errorf("adapter URL is empty")
		}
		payload, _ := json.Marshal(map[string]any{"text": title + "\n" + body, "title": title, "body": body, "severity": severity})
		// Bound every delivery: one unresponsive adapter must not stall the rest
		// of the batch, nor the background loop that runs it.
		ctx, cancel := context.WithTimeout(ctx, adapter.timeout())
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, adapter.URL, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := outboundClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("adapter returned %d", resp.StatusCode)
		}
		return nil
	default:
		return fmt.Errorf("unsupported adapter type %s", adapter.Type)
	}
}

func (a *App) listNotifications(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := a.db.Query(r.Context(), `SELECT id,kind,title,body,severity,object_type,object_id,read_at,created_at FROM notifications WHERE user_id=$1 ORDER BY read_at NULLS FIRST,created_at DESC LIMIT $2`, p.ID, parseLimit(r, 100))
	if err != nil {
		writeError(w, 500, "database_error", "알림을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, kind, title, body, severity string
		var typ, obj *string
		var read, created any
		if err := rows.Scan(&id, &kind, &title, &body, &severity, &typ, &obj, &read, &created); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "알림을 조회하지 못했습니다")
			return
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "title": title, "body": body, "severity": severity, "objectType": typ, "objectId": obj, "readAt": read, "createdAt": created})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "알림을 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) readNotification(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	tag, err := a.db.Exec(r.Context(), `UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE id=$1 AND user_id=$2`, r.PathValue("id"), p.ID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "알림을 찾을 수 없습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) readAllNotifications(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	tag, err := a.db.Exec(r.Context(), `UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE user_id=$1 AND read_at IS NULL`, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "알림을 읽음 처리하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "updated": tag.RowsAffected()})
}
