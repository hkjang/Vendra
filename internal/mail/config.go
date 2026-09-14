package mail

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The settings rows the feature reads. The names are the ones every service
// on the network uses (mail.enabled, mail.smtp_host, …), so an operator who
// has configured one of them has configured this one.
const (
	SettingCategory = "mail"

	KeyEnabled       = "mail.enabled"
	KeyHost          = "mail.smtp_host"
	KeyPort          = "mail.smtp_port"
	KeySecurity      = "mail.security"
	KeySkipTLSVerify = "mail.skip_tls_verify"
	KeyUsername      = "mail.username"
	// KeyPassword is a secret row: the value column stays empty and the
	// password lives in secret_value, encrypted, which the settings API never
	// returns.
	KeyPassword    = "mail.password"
	KeyFromAddress = "mail.from_address"
	KeyFromName    = "mail.from_name"
	KeyBaseURL     = "mail.base_url"
	KeyTimeout     = "mail.timeout_seconds"

	SecurityAuto     = "auto"
	SecurityNone     = "none"
	SecuritySTARTTLS = "starttls"
	SecurityTLS      = "tls"

	// An internal relay: port 25, no credentials, whatever encryption the
	// relay happens to offer.
	DefaultPort     = 25
	DefaultSecurity = SecurityAuto
	DefaultTimeout  = 10 * time.Second
	DefaultFromName = "Vendra"
)

// Events are the things somebody is actually waiting on. Each has a settings
// switch so an administrator can silence one kind without silencing the rest.
const (
	// EventApprovalRequested: an approval reached somebody's turn. Without it
	// the request sits until the approver happens to open the inbox, and the
	// requester waits on them.
	EventApprovalRequested = "approval.requested"
	// EventApprovalDecided: a request somebody submitted was approved,
	// rejected or returned — the thing they keep refreshing the screen for.
	EventApprovalDecided = "approval.decided"
	// EventExpiry: a contract or document is about to expire, or an
	// evaluation period is due. Missed, a contract lapses.
	EventExpiry = "expiry"
	// EventAlert: something stopped — an SLA breach, orders past a contract's
	// amount. These are the critical rows the in-app centre raises.
	EventAlert = "alert"
	// EventTest is the settings screen's test message.
	EventTest = "test"
)

// EventSwitches maps each event to the settings row that turns it off.
var EventSwitches = map[string]string{
	EventApprovalRequested: "mail.notify_approval_request",
	EventApprovalDecided:   "mail.notify_approval_decision",
	EventExpiry:            "mail.notify_expiry",
	EventAlert:             "mail.notify_alert",
}

// Setting describes one row the migration installs and the settings screen
// edits.
type Setting struct {
	Key     string
	Default any
	Secret  bool
}

// Settings lists every row, in the order the screen shows them. Off, port 25,
// no credentials: a fresh installation sends nothing and needs nothing.
var Settings = []Setting{
	{Key: KeyEnabled, Default: false},
	{Key: KeyHost, Default: ""},
	{Key: KeyPort, Default: DefaultPort},
	{Key: KeySecurity, Default: DefaultSecurity},
	{Key: KeySkipTLSVerify, Default: false},
	{Key: KeyUsername, Default: ""},
	{Key: KeyPassword, Default: "", Secret: true},
	{Key: KeyFromAddress, Default: ""},
	{Key: KeyFromName, Default: DefaultFromName},
	{Key: KeyBaseURL, Default: ""},
	{Key: KeyTimeout, Default: int(DefaultTimeout / time.Second)},
	{Key: EventSwitches[EventApprovalRequested], Default: true},
	{Key: EventSwitches[EventApprovalDecided], Default: true},
	{Key: EventSwitches[EventExpiry], Default: true},
	{Key: EventSwitches[EventAlert], Default: true},
}

// IsSettingKey reports whether a settings key belongs to this feature.
func IsSettingKey(key string) bool {
	for _, setting := range Settings {
		if setting.Key == key {
			return true
		}
	}
	return false
}

// Config is what the rows add up to.
type Config struct {
	Enabled     bool
	Host        string
	Port        int
	Security    string
	SkipVerify  bool
	Username    string
	Password    string
	FromAddress string
	FromName    string
	BaseURL     string
	Timeout     time.Duration
	// Events holds the switches that are stored. An event with no row is on:
	// adding a notification must never need a settings change first.
	Events map[string]bool
}

// Default is a fresh installation: off, an internal relay's shape.
func Default() Config {
	return Config{Port: DefaultPort, Security: DefaultSecurity, Timeout: DefaultTimeout, FromName: DefaultFromName, Events: map[string]bool{}}
}

// Parse reads the stored rows, keyed by settings key. The password is not a
// value column and is set by the caller after decrypting it. A row that does
// not parse is read as its default rather than failing the whole
// configuration: one mistyped port must not silence approvals.
func Parse(values map[string]json.RawMessage) Config {
	config := Default()
	config.Enabled = boolValue(values, KeyEnabled, false)
	config.Host = textValue(values, KeyHost, "")
	if port := intValue(values, KeyPort, DefaultPort); port > 0 {
		config.Port = port
	}
	config.Security = strings.ToLower(textValue(values, KeySecurity, DefaultSecurity))
	config.SkipVerify = boolValue(values, KeySkipTLSVerify, false)
	config.Username = textValue(values, KeyUsername, "")
	config.FromAddress = strings.ToLower(textValue(values, KeyFromAddress, ""))
	config.FromName = textValue(values, KeyFromName, DefaultFromName)
	config.BaseURL = strings.TrimRight(textValue(values, KeyBaseURL, ""), "/")
	if seconds := intValue(values, KeyTimeout, int(DefaultTimeout/time.Second)); seconds > 0 {
		config.Timeout = time.Duration(seconds) * time.Second
	}
	// The implicit-TLS port needs no extra setting.
	if config.Security == SecurityAuto && config.Port == 465 {
		config.Security = SecurityTLS
	}
	if config.FromAddress == "" && config.Host != "" {
		config.FromAddress = "vendra@" + config.Host
	}
	for event, key := range EventSwitches {
		if raw, ok := values[key]; ok {
			var enabled bool
			if json.Unmarshal(raw, &enabled) == nil {
				config.Events[event] = enabled
			}
		}
	}
	return config
}

// Allows reports whether an event kind is switched on.
func (c Config) Allows(event string) bool {
	if enabled, known := c.Events[event]; known {
		return enabled
	}
	return true
}

// Validate is what Deliver checks before it dials. The message names the
// settings row, because that is what the administrator has to correct.
func (c Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("%w: %s 가 필요합니다", ErrInvalid, KeyHost)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("%w: %s 는 1~65535 사이여야 합니다", ErrInvalid, KeyPort)
	}
	if !validAddress(c.FromAddress) {
		return fmt.Errorf("%w: %s 는 메일 주소여야 합니다", ErrInvalid, KeyFromAddress)
	}
	if !validSecurity(c.Security) {
		return fmt.Errorf("%w: %s 는 auto, none, starttls, tls 중 하나여야 합니다", ErrInvalid, KeySecurity)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("%w: %s 는 1 이상이어야 합니다", ErrInvalid, KeyTimeout)
	}
	return nil
}

// Address is the RFC 5322 From header value.
func (c Config) Address() string {
	if c.FromName != "" {
		return fmt.Sprintf("%s <%s>", c.FromName, c.FromAddress)
	}
	return c.FromAddress
}

// ValidateSetting checks one row on its way into the settings table, so a
// value the transport could never use is refused with the row named instead
// of being stored and failing every send from then on. The password row is
// not a value and is not checked here.
func ValidateSetting(key string, value json.RawMessage) error {
	switch key {
	case KeyEnabled, KeySkipTLSVerify, EventSwitches[EventApprovalRequested], EventSwitches[EventApprovalDecided], EventSwitches[EventExpiry], EventSwitches[EventAlert]:
		var b bool
		if json.Unmarshal(value, &b) != nil {
			return fmt.Errorf("%s 는 true 또는 false 여야 합니다", key)
		}
	case KeyPort:
		var n float64
		if json.Unmarshal(value, &n) != nil || n < 1 || n > 65535 || n != float64(int(n)) {
			return fmt.Errorf("%s 는 1~65535 사이의 정수여야 합니다", key)
		}
	case KeyTimeout:
		var n float64
		if json.Unmarshal(value, &n) != nil || n < 1 || n > 300 || n != float64(int(n)) {
			return fmt.Errorf("%s 는 1~300 사이의 정수여야 합니다", key)
		}
	case KeySecurity:
		var s string
		if json.Unmarshal(value, &s) != nil || !validSecurity(strings.ToLower(strings.TrimSpace(s))) {
			return fmt.Errorf("%s 는 auto, none, starttls, tls 중 하나여야 합니다", key)
		}
	case KeyFromAddress:
		var s string
		if json.Unmarshal(value, &s) != nil || (strings.TrimSpace(s) != "" && !validAddress(strings.TrimSpace(s))) {
			return fmt.Errorf("%s 는 메일 주소여야 합니다", key)
		}
	case KeyHost, KeyUsername, KeyFromName, KeyBaseURL:
		var s string
		if json.Unmarshal(value, &s) != nil || strings.ContainsAny(s, "\r\n") {
			return fmt.Errorf("%s 는 한 줄 문자열이어야 합니다", key)
		}
		if key == KeyBaseURL && strings.TrimSpace(s) != "" && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
			return fmt.Errorf("%s 는 http:// 또는 https:// 로 시작해야 합니다", key)
		}
	}
	return nil
}

func validSecurity(mode string) bool {
	switch mode {
	case SecurityAuto, SecurityNone, SecuritySTARTTLS, SecurityTLS:
		return true
	}
	return false
}

// validAddress is the shape a relay will take on MAIL FROM and RCPT TO. A
// header line break inside an address is what turns one mail into two.
func validAddress(address string) bool {
	at := strings.Index(address, "@")
	return at > 0 && at < len(address)-1 && !strings.ContainsAny(address, " \r\n<>,")
}

func boolValue(values map[string]json.RawMessage, key string, fallback bool) bool {
	var b bool
	if raw, ok := values[key]; ok && json.Unmarshal(raw, &b) == nil {
		return b
	}
	return fallback
}

func textValue(values map[string]json.RawMessage, key, fallback string) string {
	var s string
	if raw, ok := values[key]; ok && json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return fallback
}

func intValue(values map[string]json.RawMessage, key string, fallback int) int {
	var n float64
	if raw, ok := values[key]; ok && json.Unmarshal(raw, &n) == nil {
		return int(n)
	}
	return fallback
}
