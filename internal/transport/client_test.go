package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const secret = "sk-super-secret-credential-value-9876"

func authClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL: srv.URL,
		Auth: func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+secret)
			r.Header.Set("Content-Type", "application/json")
		},
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAuthInjectedAndCallerSecretsStripped(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := authClient(t, srv)
	// A caller tries to smuggle its own Authorization header; it must be dropped
	// and replaced by the injected one.
	resp, err := c.Do(context.Background(), Request{
		Method: "POST",
		Path:   "/v1/thing",
		Header: http.Header{"Authorization": []string{"Bearer attacker-supplied"}},
		Body:   []byte(`{"x":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d", resp.Status)
	}
	if gotAuth != "Bearer "+secret {
		t.Errorf("auth = %q; caller header should have been stripped and replaced", gotAuth)
	}
}

// TestSecretsNeverAppearInRecords is the secret-leak acceptance test: the
// credential must not appear anywhere in the serialized HTTP records that the
// audit log persists (GO_AGENT_PLAN.md §16).
func TestSecretsNeverAppearInRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo an auth-looking header back to also exercise response redaction.
		w.Header().Set("Set-Cookie", "session="+secret)
		w.Header().Set("Authorization", "Bearer "+secret)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := authClient(t, srv)
	resp, err := c.Do(context.Background(), Request{Method: "POST", Path: "/v1/thing", Body: []byte(`{"x":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(resp.Records)
	if strings.Contains(string(blob), secret) {
		t.Fatalf("SECRET LEAKED into HTTP records: %s", blob)
	}
	// The Authorization header must be present but redacted, not absent-by-luck.
	if !strings.Contains(string(blob), "[REDACTED]") {
		t.Errorf("expected redaction marker in records: %s", blob)
	}
}

func TestQueryAndBodyRecorded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := authClient(t, srv)
	resp, err := c.Do(context.Background(), Request{
		Method: "POST",
		Path:   "/v1/thing",
		Query:  map[string][]string{"alt": {"sse"}, "key": {secret}},
		Body:   []byte(`{"hello":"world"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := resp.Records[len(resp.Records)-1]
	// Query keys recorded, not values (so a secret in a query value can't leak
	// via the sanitized URL either).
	if len(rec.QueryKeys) != 2 {
		t.Errorf("query keys = %v", rec.QueryKeys)
	}
	if strings.Contains(rec.URL, secret) {
		t.Errorf("secret query value leaked into URL: %q", rec.URL)
	}
	if !strings.Contains(string(rec.RequestBody), "world") {
		t.Errorf("request body not recorded: %s", rec.RequestBody)
	}
}
