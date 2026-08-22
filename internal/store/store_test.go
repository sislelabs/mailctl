package store

import (
	"path/filepath"
	"testing"

	"github.com/sislelabs/mailctl/internal"
)

func TestYAMLRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mailctl.yaml")
	st := NewYAMLAt(path)

	if _, err := st.Load(); err == nil {
		t.Error("Load on a missing file should fail")
	}

	cfg := &internal.Config{
		CloudflareAPIToken: "cfut_x",
		Provider:           internal.ProviderResend,
		ResendAPIKey:       "re_x",
		DefaultForwardTo:   "you@example.com",
	}
	cfg.AddDomain("example.com", "zone1", []internal.Alias{
		{Alias: "hello", ForwardTo: []string{"you@example.com"}},
	})
	if d := cfg.FindDomain("example.com"); d != nil {
		d.ManagedDNSRecordIDs = []string{"rec1", "rec2"}
	}

	if err := st.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.SendingProvider() != internal.ProviderResend {
		t.Errorf("provider = %q", got.SendingProvider())
	}
	d := got.FindDomain("example.com")
	if d == nil {
		t.Fatal("domain did not survive the round trip")
	}
	// The tracked record IDs are what teardown keys on, so losing them in
	// serialisation would silently turn a safe teardown into a skipped one.
	if len(d.ManagedDNSRecordIDs) != 2 || d.ManagedDNSRecordIDs[0] != "rec1" {
		t.Errorf("managed record IDs = %v", d.ManagedDNSRecordIDs)
	}
}

func TestMemory(t *testing.T) {
	m := &Memory{}
	if _, err := m.Load(); err == nil {
		t.Error("empty Memory should fail to Load")
	}

	cfg := &internal.Config{DefaultForwardTo: "you@example.com"}
	if err := m.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := m.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.DefaultForwardTo != "you@example.com" {
		t.Errorf("round trip lost data: %+v", got)
	}
}

// Both implementations must satisfy Store; this is what lets a server-side
// frontend swap the backing storage without touching the flows.
var (
	_ Store = (*YAML)(nil)
	_ Store = (*Memory)(nil)
)
