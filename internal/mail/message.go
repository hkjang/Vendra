package mail

import (
	"fmt"
	"mime"
	"strings"
	"time"
)

// compose builds the MIME message. Korean subjects are encoded so relays and
// clients that predate UTF-8 headers still show them.
func compose(config Config, message Message, now time.Time) string {
	var b strings.Builder
	b.WriteString("From: " + encodeAddress(config.Address()) + "\r\n")
	b.WriteString("To: " + strings.TrimSpace(message.To) + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", message.Subject) + "\r\n")
	b.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("X-Vendra-Notification: 1\r\n")
	b.WriteString("\r\n")
	b.WriteString(normalizeBody(message.Body))
	return b.String()
}

func encodeAddress(address string) string {
	open := strings.LastIndex(address, "<")
	if open <= 0 {
		return address
	}
	return mime.QEncoding.Encode("utf-8", strings.TrimSpace(address[:open])) + " " + address[open:]
}

// normalizeBody uses CRLF line endings. Leading dots are the DATA writer's
// job (net/textproto dot-stuffs on the way out); doing it here as well would
// deliver a doubled dot.
func normalizeBody(body string) string {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	if !strings.HasSuffix(body, "\r\n") {
		body += "\r\n"
	}
	return body
}

// Notification is one event mail before its recipients are resolved.
type Notification struct {
	Event   string
	Subject string
	Lines   []string
	// Path is where the mail's link points, relative to mail.base_url. No
	// base URL, no link — the mail still makes sense without one.
	Path string
	// ObjectType and ObjectID are recorded with the delivery so the log can
	// answer "which request was that about".
	ObjectType string
	ObjectID   string
}

// Render is the message body: the lines, the link, and a footer that says why
// the mail arrived and where to turn it off.
func (n Notification) Render(config Config) string {
	lines := append([]string{}, n.Lines...)
	if link := n.link(config); link != "" {
		lines = append(lines, "", "바로 열기: "+link)
	}
	lines = append(lines, "", "—", "이 메일은 Vendra 의 메일 알림 설정에 따라 자동으로 발송되었습니다. 받지 않으려면 관리자에게 알려 주세요.")
	return strings.Join(lines, "\n")
}

func (n Notification) link(config Config) string {
	if config.BaseURL == "" || n.Path == "" {
		return ""
	}
	return config.BaseURL + "/" + strings.TrimLeft(n.Path, "/")
}

// ApprovalRequested tells the people whose turn it is that a request is
// waiting on them.
func ApprovalRequested(objectType, objectID, label, number, title, requester, step string, amount string) Notification {
	lines := []string{
		fmt.Sprintf("%s 님이 상신한 %s '%s'(%s) 이(가) %s 단계에서 결재를 기다리고 있습니다.", requester, label, title, number, step),
	}
	if amount != "" {
		lines = append(lines, "금액: "+amount)
	}
	return Notification{
		Event:      EventApprovalRequested,
		Subject:    fmt.Sprintf("[Vendra] 결재 요청: %s %s", label, title),
		Lines:      lines,
		Path:       "/approvals",
		ObjectType: objectType,
		ObjectID:   objectID,
	}
}

// ApprovalDecided tells the requester what happened to their request.
func ApprovalDecided(objectType, objectID, label, number, title, actor, action, comment, path string) Notification {
	var verb, subjectVerb string
	switch action {
	case "reject":
		verb, subjectVerb = "반려되었습니다", "반려"
	case "return":
		verb, subjectVerb = "반송되었습니다 (수정 후 다시 상신할 수 있습니다)", "반송"
	default:
		verb, subjectVerb = "최종 승인되었습니다", "승인"
	}
	lines := []string{fmt.Sprintf("상신한 %s '%s'(%s) 이(가) %s 님에 의해 %s.", label, title, number, actor, verb)}
	if strings.TrimSpace(comment) != "" {
		lines = append(lines, "", quote(comment))
	}
	return Notification{
		Event:      EventApprovalDecided,
		Subject:    fmt.Sprintf("[Vendra] %s: %s %s", subjectVerb, label, title),
		Lines:      lines,
		Path:       path,
		ObjectType: objectType,
		ObjectID:   objectID,
	}
}

// Digest bundles what one background pass raised for one person into a
// single mail. Ten expiring contracts are one message, not ten.
type DigestItem struct {
	Title string
	Body  string
}

// Digest is one mail per person per pass. The event decides the switch it
// is under and the subject; the items are the rows the in-app centre
// created in the same pass.
func Digest(event string, items []DigestItem) Notification {
	subject, heading := "[Vendra] 만료 임박 알림", "만료가 다가오는 항목입니다."
	if event == EventAlert {
		subject, heading = "[Vendra] 즉시 조치가 필요한 알림", "즉시 조치가 필요한 항목입니다."
	}
	if len(items) > 1 {
		subject = fmt.Sprintf("%s (%d건)", subject, len(items))
	}
	lines := []string{heading, ""}
	for _, item := range items {
		lines = append(lines, "• "+item.Title, "  "+item.Body)
	}
	return Notification{Event: event, Subject: subject, Lines: lines, Path: "/work"}
}

// TestMessage proves the relay works from the settings screen.
func TestMessage() Notification {
	return Notification{
		Event:   EventTest,
		Subject: "[Vendra] SMTP 시험 발송",
		Lines:   []string{"Vendra 서비스 관리 화면에서 보낸 시험 메일입니다.", "이 메일을 받았다면 SMTP 릴레이 설정이 정상입니다."},
	}
}

func quote(text string) string {
	trimmed := strings.TrimSpace(text)
	if runes := []rune(trimmed); len(runes) > 500 {
		trimmed = string(runes[:500]) + "…"
	}
	lines := strings.Split(trimmed, "\n")
	for i, line := range lines {
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
}
