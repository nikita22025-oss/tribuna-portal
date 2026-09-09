package netguard

import (
	"net/netip"
	"testing"
)

func TestPrivateTargets(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "::1", "10.0.0.1", "169.254.169.254", "::ffff:127.0.0.1", "100.64.0.1", "192.168.1.1", "224.0.0.1"} {
		if PublicIP(netip.MustParseAddr(s)) {
			t.Fatalf("allowed %s", s)
		}
	}
	if !PublicIP(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("blocked public IP")
	}
}
func TestURLSchemes(t *testing.T) {
	for _, s := range []string{"file:///etc/passwd", "http://127.0.0.1", "https://user:pass@example.com", "https://example.com:9000", "javascript:alert(1)"} {
		if ValidateURL(s) == nil {
			t.Fatalf("allowed %s", s)
		}
	}
	if ValidateURL("https://www.sports.ru/rss/all_news.xml") != nil {
		t.Fatal("blocked feed")
	}
}
