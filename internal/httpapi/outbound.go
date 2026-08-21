package httpapi

import (
	"net"
	"net/http"
	"time"
)

const (
	// outboundHardTimeout bounds any single outbound call regardless of what an
	// administrator configured, so a misconfigured integration cannot pin a
	// connection open indefinitely.
	outboundHardTimeout = 2 * time.Minute
	// defaultAdapterTimeoutSeconds is what a notification adapter gets when it
	// does not set its own.
	defaultAdapterTimeoutSeconds = 10
	maxAdapterTimeoutSeconds     = 120
)

// outboundClient is shared by every integration so connections are pooled and
// no call can hang forever. http.DefaultClient has no timeout at all: a webhook
// host that accepts the connection and never answers would block its caller
// until the process restarts.
var outboundClient = &http.Client{
	Timeout: outboundHardTimeout,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: outboundHardTimeout,
	},
}

// timeout resolves the adapter's configured budget into a usable duration.
func (a notificationAdapter) timeout() time.Duration {
	seconds := a.TimeoutSeconds
	if seconds <= 0 {
		seconds = defaultAdapterTimeoutSeconds
	}
	if seconds > maxAdapterTimeoutSeconds {
		seconds = maxAdapterTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}
