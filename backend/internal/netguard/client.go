// Package netguard prevents server-side feed/image fetches from reaching private networks.
package netguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, s := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	return true
}
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("expected public HTTP(S) URL")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !PublicIP(ip) {
		return fmt.Errorf("private network address rejected")
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return fmt.Errorf("non-web port rejected")
	}
	return nil
}
func Client(timeout time.Duration) *http.Client { return client(timeout, nil) }

// ClientWithSyntheticDNS supports environments with a DNS egress proxy mapping
// public hostnames to 240/4. Only explicitly configured hostnames are exempt;
// literal reserved addresses and all private/link-local addresses remain denied.
// Never enable this on an ordinary VPS.
func ClientWithSyntheticDNS(timeout time.Duration, hosts []string) *http.Client {
	allowed := map[string]bool{}
	for _, h := range hosts {
		allowed[strings.ToLower(h)] = true
	}
	return client(timeout, allowed)
}
func client(timeout time.Duration, syntheticHosts map[string]bool) *http.Client {
	transport := &http.Transport{
		Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConns: 12, MaxIdleConnsPerHost: 2, IdleConnTimeout: 60 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no addresses")
			}
			for _, ip := range ips {
				if !PublicIP(ip) && !(syntheticHosts[strings.ToLower(host)] && netip.MustParsePrefix("240.0.0.0/4").Contains(ip)) {
					return nil, fmt.Errorf("private network rejected for %s", host)
				}
			}
			var last error
			for _, ip := range ips {
				c, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if e == nil {
					return c, nil
				}
				last = e
			}
			return nil, last
		},
	}
	return &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return ValidateURL(req.URL.String())
	}}
}
