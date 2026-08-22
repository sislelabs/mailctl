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
	if !strings.Contains(body, "example.com") {
		t.Error("domain page missing the domain")
	}
	if !strings.Contains(body, "not registered") {
		t.Error("a domain with no Resend ID should say so")
	}
	// Addresses are read live from Cloudflare, so the page ships without them
	// and htmx fills them in. Rendering the cached config values instead would
	// show a destination that may no longer be where mail goes.
	if !strings.Contains(body, `hx-get="/domains/example.com/aliases"`) {
		t.Error("domain page should defer the alias list over htmx")
	}
}

func TestAliasListDegradesWithoutCloudflare(t *testing.T) {
	// The test config carries no usable token. The fragment must still render
	// and say so, rather than erroring the whole page.
	rec := get(t, testServer(t), "/domains/example.com/aliases")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Could not read live routing") {
		t.Errorf("expected a degraded message, got:\n%s", rec.Body.String())
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

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	return rec
}

func TestAddDomainRejectsBadInputBeforeStartingAJob(t *testing.T) {
	rec := post(t, testServer(t), "/domains", "domain=notadomain&aliases=hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "is not a domain name") {
		t.Errorf("expected validation message, got:\n%s", body)
	}
	// A rejected form must come back as the form, not as a progress view that
	// would fail on its first step.
	if strings.Contains(body, "/jobs/") {
		t.Error("no job should have been started")
	}
}

func TestAddDomainRejectsAnAlreadyConfiguredDomain(t *testing.T) {
	rec := post(t, testServer(t), "/domains", "domain=example.com&aliases=hello")
	if !strings.Contains(rec.Body.String(), "already configured") {
		t.Errorf("expected a duplicate message, got:\n%s", rec.Body.String())
	}
}

func TestUnknownJobIs404(t *testing.T) {
	if rec := get(t, testServer(t), "/jobs/deadbeef"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestRemoveConfirmShowsScope(t *testing.T) {
	rec := get(t, testServer(t), "/domains/example.com/remove")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	// The domain in the fixture has no tracked records, so the prompt must say
	// DNS is left alone rather than implying a broader sweep.
	if !strings.Contains(body, "none are tracked") {
		t.Errorf("expected the DNS scope to be spelled out, got:\n%s", body)
	}
	if !strings.Contains(body, "type example.com to confirm") {
		t.Error("expected a typed confirmation prompt")
	}
}

func TestRemoveRequiresTheExactDomainName(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/domains/example.com", strings.NewReader("confirm=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	testServer(t).ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "Type the domain name exactly") {
		t.Errorf("expected a refusal, got:\n%s", body)
	}
	if strings.Contains(body, "/jobs/") {
		t.Error("a teardown must not start without an exact confirmation")
	}
}
