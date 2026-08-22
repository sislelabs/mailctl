package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sislelabs/mailctl/internal"
	"github.com/sislelabs/mailctl/internal/store"
)

// testServer builds a panel over an in-memory config, so handler tests touch
// no filesystem and make no API calls.
func testServer(t *testing.T) http.Handler {
	t.Helper()

	cfg := &internal.Config{
		Provider:         internal.ProviderResend,
		DefaultForwardTo: "you@example.com",
	}
	cfg.AddDomain("example.com", "zone1", []internal.Alias{
		{Alias: "hello", ForwardTo: []string{"you@example.com"}},
	})

	srv, err := NewServer(&store.Memory{Config: cfg})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv.Handler()
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestOverviewRenders(t *testing.T) {
	rec := get(t, testServer(t), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"example.com", "hello@", "mailctl"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview missing %q", want)
		}
	}
	// The audit is loaded lazily; blocking the page on several API round trips
	// would make the panel feel broken on a slow network.
	if !strings.Contains(body, `hx-get="/audit"`) {
		t.Error("overview should defer the audit over htmx")
	}
}

func TestDomainPageRenders(t *testing.T) {
	rec := get(t, testServer(t), "/domains/example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hello@example.com") {
		t.Error("domain page missing its alias")
	}
	if !strings.Contains(body, "not registered") {
		t.Error("a domain with no Resend ID should say so")
	}
}

func TestUnknownDomainIs404(t *testing.T) {
	if rec := get(t, testServer(t), "/domains/nope.com"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestAddAliasRejectsBadInputWithoutCallingCloudflare(t *testing.T) {
	h := testServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/domains/example.com/aliases",
		strings.NewReader("alias=foo@bar"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)

	// The handler renders the error into the swapped fragment rather than
	// returning a bare error status, since htmx swaps the response body in.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not a valid alias") {
		t.Errorf("expected a validation message, got:\n%s", rec.Body.String())
	}
}

func TestDuplicateAliasIsRejected(t *testing.T) {
	h := testServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/domains/example.com/aliases",
		strings.NewReader("alias=hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("expected a duplicate message, got:\n%s", rec.Body.String())
	}
}

func TestStaticAssetIsServed(t *testing.T) {
	// htmx is vendored rather than pulled from a CDN so the binary is
	// self-contained and the panel works offline.
	rec := get(t, testServer(t), "/static/htmx.min.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.Len() < 1000 {
		t.Errorf("htmx looks truncated: %d bytes", rec.Body.Len())
	}
}
