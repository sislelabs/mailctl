package cmd

import "testing"

func TestResendRecordName(t *testing.T) {
	domain := "example.com"
	cases := []struct {
		in   string
		want string
	}{
		{"send", "send.example.com"},
		{"resend._domainkey", "resend._domainkey.example.com"},
		{"", "example.com"},
		{"@", "example.com"},
		{"example.com", "example.com"},
		{"links.example.com", "links.example.com"},
		{"send.example.com.", "send.example.com"}, // trailing dot trimmed
	}
	for _, c := range cases {
		if got := resendRecordName(c.in, domain); got != c.want {
			t.Errorf("resendRecordName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
