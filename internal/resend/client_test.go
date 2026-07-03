package resend

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient points a Client at a test server.
func newTestClient(url string) *Client {
	c := NewClient("re_test")
	c.httpClient = http.DefaultClient
	// Redirect baseURL by wrapping: we can't change the const, so we use a
	// RoundTripper that rewrites the host.
	c.httpClient = &http.Client{Transport: rewriteTransport{target: url}}
	return c
}

// rewriteTransport rewrites every request to hit the test server instead of
// the real api.resend.com host, preserving the path.
type rewriteTransport struct{ target string }

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := req.URL.Parse(t.target + req.URL.Path)
	req.URL = u
	return http.DefaultTransport.RoundTrip(req)
}

func TestAddDomainParsesRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/domains" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer re_test" {
			t.Errorf("missing/incorrect auth header: %q", got)
		}
		io.WriteString(w, `{
			"id": "d91cd9bd-1176-453e-8fc1-35364d380206",
			"name": "example.com",
			"status": "not_started",
			"records": [
				{"record":"SPF","name":"send","type":"MX","value":"feedback-smtp.us-east-1.amazonses.com","priority":10,"status":"not_started"},
				{"record":"DKIM","name":"resend._domainkey","type":"TXT","value":"p=MIGf...","status":"not_started"}
			]
		}`)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	d, err := c.AddDomain("example.com")
	if err != nil {
		t.Fatalf("AddDomain: %v", err)
	}
	if d.ID != "d91cd9bd-1176-453e-8fc1-35364d380206" {
		t.Errorf("id = %q", d.ID)
	}
	if len(d.Records) != 2 {
		t.Fatalf("got %d records, want 2", len(d.Records))
	}
	if d.Records[0].Priority != 10 || d.Records[0].Type != "MX" {
		t.Errorf("MX record parsed wrong: %+v", d.Records[0])
	}
}

func TestSendEmailBody(t *testing.T) {
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emails" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"id":"abc"}`)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	err := c.SendEmail(SendParams{
		From:    "hello@example.com",
		To:      []string{"you@gmail.com"},
		Subject: "Hi",
		Text:    "Body text",
		Attachments: []Attachment{
			{Filename: "a.txt", Content: []byte("hello"), ContentType: "text/plain"},
		},
	})
	if err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	if body["from"] != "hello@example.com" || body["subject"] != "Hi" || body["text"] != "Body text" {
		t.Errorf("body fields wrong: %+v", body)
	}
	to, ok := body["to"].([]interface{})
	if !ok || len(to) != 1 || to[0] != "you@gmail.com" {
		t.Errorf("to wrong: %+v", body["to"])
	}
	atts, ok := body["attachments"].([]interface{})
	if !ok || len(atts) != 1 {
		t.Fatalf("attachments wrong: %+v", body["attachments"])
	}
	att := atts[0].(map[string]interface{})
	// "hello" base64-encoded is "aGVsbG8=".
	if att["content"] != "aGVsbG8=" || att["filename"] != "a.txt" {
		t.Errorf("attachment encoded wrong: %+v", att)
	}
}

func TestErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		io.WriteString(w, `{"message":"domain already exists"}`)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.AddDomain("example.com")
	if err == nil {
		t.Fatal("expected error on 422")
	}
}
