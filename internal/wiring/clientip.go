package wiring

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP returns the function that finds the caller's IP for rate limits.
//
// With header empty it is the host of RemoteAddr. With header set (the
// ingress in front of the API, e.g. "X-Forwarded-For"), it is the right-most
// value of that header: the one the trusted proxy appended itself. Values to
// its left come from the client and can be forged, so they are never used.
// When the header is missing or its right-most value is not an IP address,
// RemoteAddr is used.
func ClientIP(header string) func(*http.Request) string {
	if header == "" {
		return remoteHost
	}
	return func(r *http.Request) string {
		values := r.Header.Values(header)
		if len(values) == 0 {
			return remoteHost(r)
		}
		last := values[len(values)-1]
		if i := strings.LastIndexByte(last, ','); i >= 0 {
			last = last[i+1:]
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(last))
		if err != nil {
			return remoteHost(r)
		}
		return addr.Unmap().String()
	}
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
