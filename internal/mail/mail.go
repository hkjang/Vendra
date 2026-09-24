// Package mail sends event notifications through a company SMTP relay.
//
// An internal relay commonly takes mail on port 25 with no credentials and no
// TLS, so those are the defaults and both authentication and encryption are
// negotiated only as far as the relay offers them. Nothing here holds a
// request: a notification is sent in the background and every attempt is
// recorded, so an administrator can see what left the building and answer
// "it never arrived" with a row rather than a shrug.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

var (
	ErrDisabled = errors.New("mail is disabled")
	ErrInvalid  = errors.New("invalid mail configuration")
)

// Message is one mail ready for the relay.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Deliver opens a connection and sends one message. It is what the settings
// screen's test button calls, so a relay can be proven before anything
// depends on it.
func Deliver(ctx context.Context, config Config, message Message) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(message.To) == "" {
		return fmt.Errorf("%w: recipient is required", ErrInvalid)
	}
	client, err := dial(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err := startSession(client, config); err != nil {
		return err
	}
	if err := client.Mail(config.FromAddress); err != nil {
		return fmt.Errorf("MAIL FROM 실패: %w", err)
	}
	if err := client.Rcpt(strings.TrimSpace(message.To)); err != nil {
		return fmt.Errorf("RCPT TO 실패: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA 실패: %w", err)
	}
	if _, err := writer.Write([]byte(compose(config, message, time.Now()))); err != nil {
		return fmt.Errorf("본문 전송 실패: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("본문 종료 실패: %w", err)
	}
	return client.Quit()
}

func dial(ctx context.Context, config Config) (*smtp.Client, error) {
	dialer := &net.Dialer{Timeout: config.Timeout}
	endpoint := net.JoinHostPort(config.Host, fmt.Sprint(config.Port))
	var connection net.Conn
	var err error
	if config.Security == SecurityTLS {
		connection, err = tls.DialWithDialer(dialer, "tcp", endpoint, config.tlsConfig())
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", endpoint)
	}
	if err != nil {
		return nil, fmt.Errorf("SMTP 연결 실패: %w", err)
	}
	// The dialer's timeout covers the connection only; the deadline covers the
	// whole conversation, so a relay that accepts and then stalls cannot hold
	// the sending goroutine for good.
	_ = connection.SetDeadline(time.Now().Add(config.Timeout))
	client, err := smtp.NewClient(connection, config.Host)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("SMTP 세션 시작 실패: %w", err)
	}
	return client, nil
}

// startSession upgrades and authenticates only as far as the relay allows, so
// an unauthenticated internal relay works with the same settings as a hosted
// provider that demands both.
func startSession(client *smtp.Client, config Config) error {
	if err := client.Hello(config.helloName()); err != nil {
		return fmt.Errorf("EHLO 실패: %w", err)
	}
	if config.Security == SecuritySTARTTLS || config.Security == SecurityAuto {
		if supported, _ := client.Extension("STARTTLS"); supported {
			if err := client.StartTLS(config.tlsConfig()); err != nil {
				return fmt.Errorf("STARTTLS 실패: %w", err)
			}
		} else if config.Security == SecuritySTARTTLS {
			return fmt.Errorf("%w: 서버가 STARTTLS를 지원하지 않습니다", ErrInvalid)
		}
	}
	if config.Username == "" {
		return nil
	}
	supported, mechanisms := client.Extension("AUTH")
	if !supported {
		return fmt.Errorf("%w: 서버가 인증을 지원하지 않습니다. 사용자 이름을 비우고 사용하세요", ErrInvalid)
	}
	mechanisms = strings.ToUpper(mechanisms)
	switch {
	case strings.Contains(mechanisms, "PLAIN"):
		return client.Auth(smtp.PlainAuth("", config.Username, config.Password, config.Host))
	case strings.Contains(mechanisms, "LOGIN"):
		return client.Auth(loginAuth{username: config.Username, password: config.Password, host: config.Host})
	default:
		return client.Auth(smtp.CRAMMD5Auth(config.Username, config.Password))
	}
}

func (c Config) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.SkipVerify} //nolint:gosec // opt-in for relays with a private certificate
}

// helloName keeps the EHLO name to the sender's domain, which relays that
// check the greeting accept more readily than a container hostname.
func (c Config) helloName() string {
	if at := strings.LastIndex(c.FromAddress, "@"); at >= 0 && at+1 < len(c.FromAddress) {
		return c.FromAddress[at+1:]
	}
	return "localhost"
}

// loginAuth is the LOGIN mechanism several corporate relays offer instead of
// PLAIN. The standard library ships only PLAIN and CRAM-MD5.
type loginAuth struct{ username, password, host string }

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && server.Name != a.host {
		return "", nil, errors.New("LOGIN 인증은 신뢰할 수 있는 서버에서만 사용합니다")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimRight(string(fromServer), ": ")) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("알 수 없는 LOGIN 요청: %s", fromServer)
}
