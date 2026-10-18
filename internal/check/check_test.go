package check

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Blackman99/keyroll/internal/provider"
)

// Answers recorded from the real APIs on 2026-10-18 with an obviously fake key.
func TestRecordedProviderAnswersAreRejected(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.json")
	if len(files) < 10 {
		t.Fatalf("expected recorded fixtures, found %d", len(files))
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		var fx struct {
			Provider string `json:"provider"`
			Status   int    `json:"status"`
			Body     string `json:"body"`
		}
		if err := json.Unmarshal(b, &fx); err != nil {
			t.Fatal(f, err)
		}
		if provider.ByID(fx.Provider) == nil {
			t.Errorf("%s: fixture for unknown provider %q", f, fx.Provider)
		}
		if r := Interpret(fx.Provider, fx.Status, []byte(fx.Body)); r.State != Rejected {
			t.Errorf("%s: %d → %s (%s), want dead", fx.Provider, fx.Status, r.State, r.Detail)
		}
	}
}

func TestInterpret(t *testing.T) {
	cases := []struct {
		id     string
		status int
		body   string
		want   State
	}{
		{"openai", 200, `{"data":[]}`, Alive},
		{"anthropic", 429, `{}`, Unknown}, // a bare 429 can come before auth
		{"openai", 429, `{"error":{"code":"insufficient_quota"}}`, Alive},
		{"anthropic", 401, `{"type":"error","error":{"type":"authentication_error"}}`, Rejected},
		{"anthropic", 529, `{}`, Unknown},
		{"openai", 403, `{"error":{"code":"unsupported_country_region_territory"}}`, Unknown},
		{"google", 403, `{"error":{"status":"PERMISSION_DENIED","details":[{"reason":"SERVICE_DISABLED"}]}}`, Alive},
		{"google", 400, `{"error":{"message":"Invalid JSON payload"}}`, Unknown},
		{"xai", 200, `{"api_key_blocked":false,"api_key_disabled":true}`, Rejected},
	}
	for _, c := range cases {
		if got := Interpret(c.id, c.status, []byte(c.body)).State; got != c.want {
			t.Errorf("%s %d: got %s want %s", c.id, c.status, got, c.want)
		}
	}
	r := Interpret("openrouter", 200, []byte(`{"data":{"label":"x","limit":null,"usage":412.2}}`))
	if !r.NoCap || r.Usage == nil || *r.Usage != 412.2 || !strings.Contains(r.Detail, "no spend limit") {
		t.Fatalf("openrouter cap: %+v", r)
	}
	r = Interpret("openrouter", 200, []byte(`{"data":{"limit":20,"usage":3}}`))
	if r.NoCap || !strings.Contains(r.Detail, "limit $20.00") {
		t.Fatalf("openrouter limit: %+v", r)
	}
}

func TestKeyGoesInHeaderOnlyAndRedirectsAreNotFollowed(t *testing.T) {
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
		w.WriteHeader(200)
	}))
	defer other.Close()
	var gotHeader, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-goog-api-key")
		gotQuery = r.URL.RawQuery
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer srv.Close()
	c := New("test")
	c.Override = map[string]string{"google": srv.URL + "/v1beta/models?pageSize=1"}
	res := c.One(context.Background(), provider.ByID("google"), provider.KindAPI, "AIza-test-value")
	if gotHeader != "AIza-test-value" || strings.Contains(gotQuery, "AIza") {
		t.Fatalf("key must travel in x-goog-api-key, not the URL: header=%q query=%q", gotHeader, gotQuery)
	}
	if leaked.Load() != 0 {
		t.Fatal("redirect was followed: the key could reach another host")
	}
	if res.State != Unknown {
		t.Fatalf("a redirect is not an answer: %+v", res)
	}
}

func TestSkips(t *testing.T) {
	c := New("test")
	for _, tc := range []struct {
		p    *provider.Provider
		kind provider.Kind
	}{
		{provider.Unknown, provider.KindAPI},
		{provider.ByID("anthropic"), provider.KindLogin},
		{provider.ByID("perplexity"), provider.KindAPI},
	} {
		if r := c.One(context.Background(), tc.p, tc.kind, "x"); r.State != Skipped {
			t.Errorf("%s/%s should be skipped, got %s", tc.p.ID, tc.kind, r.State)
		}
	}
}
