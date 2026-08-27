package mailsetup

import (
	"reflect"
	"strings"
	"testing"
)

func TestZoneCandidates(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		// A sending subdomain resolves by walking up to the zone that holds it.
		{"info.getsaiton.com", []string{"info.getsaiton.com", "getsaiton.com"}},
		{"example.com", []string{"example.com"}},
		{"a.b.c.example.com", []string{
			"a.b.c.example.com", "b.c.example.com", "c.example.com", "example.com",
		}},
		// The walk stops at two labels; a bare TLD is never a zone.
		{"example.co.uk", []string{"example.co.uk", "co.uk"}},
		{"INFO.Example.COM", []string{"info.example.com", "example.com"}},
		{"example.com.", []string{"example.com"}},
		{"localhost", nil},
		{"", nil},
	}

	for _, c := range cases {
		if got := ZoneCandidates(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ZoneCandidates(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRoutingMXHostMatchesCloudflare(t *testing.T) {
	// The apex MX check keys on this suffix; Cloudflare publishes
	// route1/2/3.mx.cloudflare.net when Email Routing is enabled.
	for _, host := range []string{
		"route1.mx.cloudflare.net",
		"route2.mx.cloudflare.net",
		"route3.mx.cloudflare.net",
	} {
		if !strings.Contains(host, RoutingMXHost) {
			t.Errorf("%q should contain %q", host, RoutingMXHost)
		}
	}
	// A provider return-path MX must not be mistaken for routing.
	if strings.Contains("feedback-smtp.us-east-1.amazonses.com", RoutingMXHost) {
		t.Error("a return-path MX must not look like a routing MX")
	}
}
