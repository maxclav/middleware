package redirect_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxclav/middleware/redirect"
)

// okHandler records that it ran and writes 200 OK.
func okHandler(hit *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*hit = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestRedirect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		opts         []redirect.Option
		target       string // request URL
		forwarded    string // X-Forwarded-Proto header, empty to omit
		wantHit      bool   // whether the next handler runs (no redirect)
		wantStatus   int
		wantLocation string
	}{
		{
			name:         "http to https",
			opts:         []redirect.Option{redirect.WithScheme("https")},
			target:       "http://example.com/foo",
			wantStatus:   http.StatusPermanentRedirect,
			wantLocation: "https://example.com/foo",
		},
		{
			name:         "non-canonical host",
			opts:         []redirect.Option{redirect.WithHost("www.example.com")},
			target:       "http://example.com/",
			wantStatus:   http.StatusPermanentRedirect,
			wantLocation: "http://www.example.com/",
		},
		{
			name: "already canonical passes through",
			opts: []redirect.Option{
				redirect.WithScheme("http"),
				redirect.WithHost("example.com"),
			},
			target:     "http://example.com/foo",
			wantHit:    true,
			wantStatus: http.StatusOK,
		},
		{
			name:         "query string preserved",
			opts:         []redirect.Option{redirect.WithScheme("https")},
			target:       "http://example.com/search?q=go&p=2",
			wantStatus:   http.StatusPermanentRedirect,
			wantLocation: "https://example.com/search?q=go&p=2",
		},
		{
			name: "custom redirect code",
			opts: []redirect.Option{
				redirect.WithScheme("https"),
				redirect.WithCode(http.StatusMovedPermanently),
			},
			target:       "http://example.com/",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "https://example.com/",
		},
		{
			name: "trusted forwarded proto avoids redirect",
			opts: []redirect.Option{
				redirect.WithScheme("https"),
				redirect.WithTrustForwardedHeaders(true),
			},
			target:     "http://example.com/",
			forwarded:  "https",
			wantHit:    true,
			wantStatus: http.StatusOK,
		},
		{
			name: "trusted forwarded proto http still redirects",
			opts: []redirect.Option{
				redirect.WithScheme("https"),
				redirect.WithTrustForwardedHeaders(true),
			},
			target:       "http://example.com/",
			forwarded:    "http",
			wantStatus:   http.StatusPermanentRedirect,
			wantLocation: "https://example.com/",
		},
		{
			name:         "forwarded proto ignored when untrusted",
			opts:         []redirect.Option{redirect.WithScheme("https")},
			target:       "http://example.com/",
			forwarded:    "https",
			wantStatus:   http.StatusPermanentRedirect,
			wantLocation: "https://example.com/",
		},
		{
			// An https:// URL sets r.TLS, so requestScheme reports "https" and
			// a canonical "https" scheme passes the request through unchanged.
			name:       "tls request already https passes through",
			opts:       []redirect.Option{redirect.WithScheme("https")},
			target:     "https://example.com/foo",
			wantHit:    true,
			wantStatus: http.StatusOK,
		},
		{
			// No scheme or host configured => nothing is ever canonicalized.
			name:       "no canonical config never redirects",
			opts:       nil,
			target:     "http://example.com/foo",
			wantHit:    true,
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mw, err := redirect.New(tt.opts...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			var hit bool
			h := mw(okHandler(&hit))

			req := httptest.NewRequest(http.MethodGet, tt.target, http.NoBody)
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-Proto", tt.forwarded)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if hit != tt.wantHit {
				t.Errorf("next handler hit = %v, want %v", hit, tt.wantHit)
			}
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantLocation != "" {
				if got := rec.Header().Get("Location"); got != tt.wantLocation {
					t.Errorf("Location = %q, want %q", got, tt.wantLocation)
				}
			}
		})
	}
}

func TestOptionValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opt     redirect.Option
		wantErr bool
	}{
		{name: "invalid scheme", opt: redirect.WithScheme("ftp"), wantErr: true},
		{name: "http scheme valid", opt: redirect.WithScheme("http"), wantErr: false},
		{name: "https scheme valid", opt: redirect.WithScheme("https"), wantErr: false},
		{name: "empty host", opt: redirect.WithHost(""), wantErr: true},
		{name: "host valid", opt: redirect.WithHost("example.com"), wantErr: false},
		{name: "non-3xx code", opt: redirect.WithCode(http.StatusOK), wantErr: true},
		{name: "code below 300", opt: redirect.WithCode(299), wantErr: true},
		{name: "code above 399", opt: redirect.WithCode(400), wantErr: true},
		{name: "code 3xx valid", opt: redirect.WithCode(http.StatusFound), wantErr: false},
		{name: "trust forwarded valid", opt: redirect.WithTrustForwardedHeaders(true), wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := redirect.New(tt.opt)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestMultipleInvalidOptionsJoined(t *testing.T) {
	t.Parallel()

	// Several invalid options are joined into a single error.
	if _, err := redirect.New(redirect.WithScheme("ftp"), redirect.WithHost("")); err == nil {
		t.Fatal("expected joined error for multiple invalid options")
	}
}
