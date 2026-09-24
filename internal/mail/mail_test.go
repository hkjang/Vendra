package mail

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRelay is the least SMTP server that can take a message. It keeps the
// conversation so a test can read what Vendra actually said — including
// whether it tried to authenticate.
type fakeRelay struct {
	listener   net.Listener
	offerAuth  bool
	rejectFrom bool
	mu         sync.Mutex
	commands   []string
	body       string
}

func startRelay(t *testing.T, offerAuth bool) *fakeRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	relay := &fakeRelay{listener: listener, offerAuth: offerAuth}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go relay.handle(connection)
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return relay
}

func (f *fakeRelay) hostPort() (string, int) {
	host, port, _ := net.SplitHostPort(f.listener.Addr().String())
	n := 0
	_, _ = fmt.Sscanf(port, "%d", &n)
	return host, n
}

func (f *fakeRelay) transcript() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.commands, "\n")
}

func (f *fakeRelay) message() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body
}

func (f *fakeRelay) handle(connection net.Conn) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	write := func(line string) { _, _ = connection.Write([]byte(line + "\r\n")) }
	write("220 relay.internal ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimSpace(line)
		f.mu.Lock()
		f.commands = append(f.commands, command)
		f.mu.Unlock()
		upper := strings.ToUpper(command)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			write("250-relay.internal")
			if f.offerAuth {
				write("250-AUTH PLAIN LOGIN")
			}
			write("250 SIZE 35882577")
		case strings.HasPrefix(upper, "AUTH"):
			write("235 2.7.0 Authentication successful")
		case strings.HasPrefix(upper, "MAIL FROM"):
			if f.rejectFrom {
				write("550 5.7.1 Sender rejected")
				continue
			}
			write("250 2.1.0 Ok")
		case upper == "DATA":
			write("354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				body.WriteString(dataLine)
			}
			f.mu.Lock()
			f.body = body.String()
			f.mu.Unlock()
			write("250 2.0.0 Ok: queued")
		case upper == "QUIT":
			write("221 2.0.0 Bye")
			return
		default:
			write("250 2.0.0 Ok")
		}
	}
}

func relayConfig(relay *fakeRelay) Config {
	config := Default()
	config.Enabled = true
	config.Host, config.Port = relay.hostPort()
	config.FromAddress, config.FromName = "vendra@corp.example", "Vendra 알림"
	config.Timeout = 3 * time.Second
	return config
}

// An internal relay that asks for nothing must work with no credentials, and
// nothing must be attempted that it did not offer.
func TestAnInternalRelayNeedsNoCredentials(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	err := Deliver(context.Background(), relayConfig(relay), Message{To: "park@corp.example", Subject: "결재 요청", Body: "본문입니다.\n.점으로 시작하는 줄"})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	transcript := relay.transcript()
	if !strings.Contains(transcript, "EHLO corp.example") || !strings.Contains(transcript, "MAIL FROM:<vendra@corp.example>") || !strings.Contains(transcript, "RCPT TO:<park@corp.example>") {
		t.Fatalf("transcript:\n%s", transcript)
	}
	if strings.Contains(strings.ToUpper(transcript), "AUTH ") {
		t.Fatal("no credentials were configured, so no AUTH should have been attempted")
	}
	body := relay.message()
	// The subject is encoded, the leading dot is escaped, and the mail says
	// what it is.
	if strings.Contains(body, "...점으로") || !strings.Contains(body, "\r\n..점으로 시작하는 줄") {
		t.Errorf("the leading dot should be stuffed exactly once on the wire:\n%s", body)
	}
	for _, want := range []string{"Subject: =?utf-8?q?", "Content-Type: text/plain; charset=UTF-8", "Auto-Submitted: auto-generated", "X-Vendra-Notification: 1", "From: =?utf-8?q?Vendra_=EC=95=8C=EB=A6=BC?= <vendra@corp.example>"} {
		if !strings.Contains(body, want) {
			t.Errorf("message lacks %q:\n%s", want, body)
		}
	}
}

func TestCredentialsAreUsedWhenTheRelayOffersAuth(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, true)
	config := relayConfig(relay)
	config.Username, config.Password = "vendra", "relay-pass"
	if err := Deliver(context.Background(), config, Message{To: "lee@corp.example", Subject: "알림", Body: "본문"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if !strings.Contains(strings.ToUpper(relay.transcript()), "AUTH PLAIN") {
		t.Fatalf("transcript:\n%s", relay.transcript())
	}
}

// Credentials against a relay that cannot take them are a configuration
// mistake, and the message says which box to empty.
func TestCredentialsAgainstARelayWithoutAuthAreExplained(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	config := relayConfig(relay)
	config.Username, config.Password = "vendra", "relay-pass"
	err := Deliver(context.Background(), config, Message{To: "lee@corp.example", Subject: "알림", Body: "본문"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "사용자 이름을 비우고") {
		t.Fatalf("error = %v", err)
	}
}

func TestARejectedSenderIsReported(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	relay.rejectFrom = true
	err := Deliver(context.Background(), relayConfig(relay), Message{To: "lee@corp.example", Subject: "알림", Body: "본문"})
	if err == nil || !strings.Contains(err.Error(), "MAIL FROM") {
		t.Fatalf("error = %v", err)
	}
}

// A relay that is not there is an error, not a hang: the deadline is the
// configured timeout.
func TestADeadRelayFailsWithinTheTimeout(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	config := Default()
	config.Host, config.Port = func() (string, int) {
		host, port, _ := net.SplitHostPort(address)
		n := 0
		_, _ = fmt.Sscanf(port, "%d", &n)
		return host, n
	}()
	config.FromAddress = "vendra@corp.example"
	config.Timeout = time.Second
	started := time.Now()
	err = Deliver(context.Background(), config, Message{To: "lee@corp.example", Subject: "알림", Body: "본문"})
	if err == nil || !strings.Contains(err.Error(), "SMTP 연결 실패") {
		t.Fatalf("error = %v", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("a dead relay held the send for %s", time.Since(started))
	}
}

func values(pairs map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for key, value := range pairs {
		raw, _ := json.Marshal(value)
		out[key] = raw
	}
	return out
}

func TestTheDefaultsAreAnInternalRelayAndOff(t *testing.T) {
	t.Parallel()
	config := Parse(nil)
	if config.Enabled || config.Port != 25 || config.Security != SecurityAuto || config.Timeout != 10*time.Second || config.Username != "" {
		t.Fatalf("defaults = %+v", config)
	}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), KeyHost) {
		t.Fatalf("an empty host validates as %v, want the row named", err)
	}
	// Port 465 is implicit TLS without anybody having to say so; a missing
	// sender is derived from the host so the setup stays short.
	config = Parse(values(map[string]any{KeyEnabled: true, KeyHost: "relay.internal", KeyPort: 465}))
	if !config.Enabled || config.Security != SecurityTLS || config.FromAddress != "vendra@relay.internal" {
		t.Fatalf("config = %+v", config)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	// A row that does not parse falls back rather than silencing everything.
	config = Parse(values(map[string]any{KeyHost: "relay.internal", KeyPort: "twenty-five", KeyTimeout: -3}))
	if config.Port != 25 || config.Timeout != 10*time.Second {
		t.Fatalf("config = %+v", config)
	}
	// Event switches: absent is on, stored false is off.
	config = Parse(values(map[string]any{EventSwitches[EventExpiry]: false}))
	if config.Allows(EventExpiry) || !config.Allows(EventApprovalRequested) {
		t.Fatal("event switches are not read")
	}
	for _, setting := range Settings {
		if setting.Secret {
			continue
		}
		raw, _ := json.Marshal(setting.Default)
		if err := ValidateSetting(setting.Key, raw); err != nil {
			t.Errorf("the installed default for %s is refused: %v", setting.Key, err)
		}
	}
	for key, bad := range map[string]any{KeyPort: 70000, KeySecurity: "quantum", KeyFromAddress: "nobody", KeyTimeout: 0, KeyEnabled: "yes", KeyBaseURL: "vendra.corp", KeyHost: "relay\r\nX-Injected: 1"} {
		raw, _ := json.Marshal(bad)
		if err := ValidateSetting(key, raw); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%v was accepted (%v), want a refusal naming the row", key, bad, err)
		}
	}
}

type fakeDirectory map[string]string

func (d fakeDirectory) Emails(_ context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if email, ok := d[id]; ok {
			out[id] = email
		}
	}
	return out, nil
}

type recordingSender struct {
	mu   sync.Mutex
	sent []Message
	fail error
}

func (r *recordingSender) send(_ context.Context, _ Config, message Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.sent = append(r.sent, message)
	return nil
}

func (r *recordingSender) recipients() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, message := range r.sent {
		out = append(out, message.To)
	}
	return out
}

func serviceWith(config Config, sender *recordingSender) *Service {
	service := NewService(nil, func(context.Context) (Config, error) { return config, nil },
		fakeDirectory{"u-actor": "actor@corp.example", "u-kim": "kim@corp.example", "u-lee": "lee@corp.example", "u-kim-again": "KIM@corp.example"})
	service.SetSender(sender.send)
	return service
}

// The five things the standard asks a test to show, without a relay: off
// sends nothing, a switched-off event sends nothing, the actor is never told
// about their own action, an address is written to once, and an
// unresolvable id is skipped quietly.
func TestNotifyIsQuietWhenItShouldBe(t *testing.T) {
	t.Parallel()
	config := Default()
	config.Enabled, config.Host, config.FromAddress = true, "relay.internal", "vendra@corp.example"
	notification := ApprovalRequested("contract", "obj-1", "계약", "CT-1", "연간 유지보수", "박지민", "팀장 승인", "1000000 KRW")

	sender := &recordingSender{}
	off := config
	off.Enabled = false
	serviceWith(off, sender).Notify(context.Background(), notification, "u-actor", []string{"u-kim"})
	if len(sender.recipients()) != 0 {
		t.Fatal("mail went out while the feature is off")
	}

	muted := config
	muted.Events = map[string]bool{EventApprovalRequested: false}
	service := serviceWith(muted, sender)
	service.Notify(context.Background(), notification, "u-actor", []string{"u-kim"})
	service.Notify(context.Background(), TestMessage(), "u-actor", []string{"u-kim"})
	service.Wait()
	if got := sender.recipients(); len(got) != 1 {
		t.Fatalf("with one event muted, recipients = %v, want only the other event's", got)
	}

	sender = &recordingSender{}
	service = serviceWith(config, sender)
	service.Notify(context.Background(), notification, "u-actor", []string{"u-actor", "u-kim", "u-kim", "u-kim-again", "u-gone", "u-lee"})
	service.Wait()
	got := sender.recipients()
	sort.Strings(got)
	if strings.Join(got, ",") != "kim@corp.example,lee@corp.example" {
		t.Fatalf("recipients = %v, want kim and lee once each, never the actor", got)
	}
	if !strings.Contains(sender.sent[0].Body, "연간 유지보수") || strings.Contains(sender.sent[0].Body, "바로 열기") {
		t.Fatalf("body = %q", sender.sent[0].Body)
	}
}

// Enabled but unusable is not a crash and not a hang: the request goes on
// and the reason is what the log carries.
func TestAnUnusableConfigurationNeverFailsTheCaller(t *testing.T) {
	t.Parallel()
	config := Default()
	config.Enabled = true // no host
	sender := &recordingSender{}
	service := serviceWith(config, sender)
	service.Notify(context.Background(), TestMessage(), "u-actor", []string{"u-kim"})
	service.Wait()
	if len(sender.recipients()) != 0 {
		t.Fatal("a send was attempted with no host")
	}
	if err := service.SendNow(context.Background(), TestMessage(), "u-actor", "kim@corp.example"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SendNow = %v, want ErrInvalid", err)
	}
	broken := NewService(nil, func(context.Context) (Config, error) { return Config{}, errors.New("settings unavailable") }, fakeDirectory{})
	broken.SetSender(sender.send)
	broken.Notify(context.Background(), TestMessage(), "", []string{"u-kim"})
	broken.Wait()
}

func TestABodyLinksBackWhenABaseURLIsSet(t *testing.T) {
	t.Parallel()
	config := Config{BaseURL: "https://vendra.corp.example"}
	body := ApprovalDecided("purchase_order", "obj-2", "발주", "PO-7", "서버 12대", "이수진", "reject", "예산 초과\n재검토 바람", "/purchase-orders").Render(config)
	for _, want := range []string{"반려되었습니다", "> 예산 초과", "> 재검토 바람", "바로 열기: https://vendra.corp.example/purchase-orders", "자동으로 발송되었습니다"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	digest := Digest(EventExpiry, []DigestItem{{Title: "계약 종료 7일 이내", Body: "A 계약이 2026-09-21 종료됩니다."}, {Title: "문서 만료 7일 이내", Body: "ISO 인증서 문서가 2026-09-20 만료됩니다."}})
	if !strings.Contains(digest.Subject, "(2건)") || !strings.Contains(digest.Render(config), "• 계약 종료 7일 이내") {
		t.Fatalf("digest = %+v", digest)
	}
	if !strings.Contains(Digest(EventAlert, nil).Subject, "즉시 조치") {
		t.Fatal("an alert digest should say so in the subject")
	}
}
