// Package secure provides middleware that sets a baseline of security-related
// response headers on every request.
//
// The zero-configuration constructor [New] applies a sensible default set
// (X-Content-Type-Options, X-Frame-Options and Referrer-Policy) while leaving
// deliberately opt-in policies such as HSTS, Content-Security-Policy and
// Permissions-Policy off until they are configured with the matching options.
package secure

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/maxclav/middleware"
)

type config struct {
	hstsMaxAge            time.Duration
	hstsIncludeSubdomains bool
	hstsPreload           bool
	trustForwardedProto   bool

	frameOptions          string
	contentTypeNosniff    bool
	referrerPolicy        string
	contentSecurityPolicy string
	permissionsPolicy     string
}

// Option configures the secure middleware.
type Option func(*config) error

// WithHSTS enables the Strict-Transport-Security header with the given max-age
// and flags. HSTS is emitted only when the request is served over TLS (or, when
// [WithTrustForwardedProto] is enabled, when X-Forwarded-Proto is "https").
// A non-positive maxAge leaves HSTS disabled, which is the default.
func WithHSTS(maxAge time.Duration, includeSubdomains, preload bool) Option {
	return func(c *config) error {
		if maxAge < 0 {
			return errors.New("secure: HSTS max-age must not be negative")
		}
		c.hstsMaxAge = maxAge
		c.hstsIncludeSubdomains = includeSubdomains
		c.hstsPreload = preload
		return nil
	}
}

// WithTrustForwardedProto controls whether the X-Forwarded-Proto request header
// is trusted when deciding if a request is secure for HSTS purposes. Enable it
// only behind a proxy that sets the header reliably. Defaults to false.
func WithTrustForwardedProto(trust bool) Option {
	return func(c *config) error {
		c.trustForwardedProto = trust
		return nil
	}
}

// WithFrameOptions sets the X-Frame-Options header. Valid values are "DENY",
// "SAMEORIGIN" or "" to disable the header. Defaults to "DENY".
func WithFrameOptions(value string) Option {
	return func(c *config) error {
		switch value {
		case "", "DENY", "SAMEORIGIN":
			c.frameOptions = value
			return nil
		default:
			return fmt.Errorf("secure: invalid frame options %q, want DENY, SAMEORIGIN or empty", value)
		}
	}
}

// WithContentTypeNosniff controls whether the X-Content-Type-Options: nosniff
// header is emitted. Defaults to true.
func WithContentTypeNosniff(enabled bool) Option {
	return func(c *config) error {
		c.contentTypeNosniff = enabled
		return nil
	}
}

// WithReferrerPolicy sets the Referrer-Policy header. An empty value disables
// the header. Defaults to "no-referrer".
func WithReferrerPolicy(value string) Option {
	return func(c *config) error {
		c.referrerPolicy = value
		return nil
	}
}

// WithContentSecurityPolicy sets the Content-Security-Policy header. An empty
// value disables the header, which is the default.
func WithContentSecurityPolicy(value string) Option {
	return func(c *config) error {
		c.contentSecurityPolicy = value
		return nil
	}
}

// WithPermissionsPolicy sets the Permissions-Policy header. An empty value
// disables the header, which is the default.
func WithPermissionsPolicy(value string) Option {
	return func(c *config) error {
		c.permissionsPolicy = value
		return nil
	}
}

// New returns middleware that writes the configured security headers on the
// response before invoking the next handler. A header is written only when its
// configured value is non-empty or its feature is enabled.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		frameOptions:       "DENY",
		contentTypeNosniff: true,
		referrerPolicy:     "no-referrer",
	}
	var errs []error
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	hstsValue := cfg.hstsHeaderValue()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			if hstsValue != "" && cfg.isSecure(r) {
				h.Set("Strict-Transport-Security", hstsValue)
			}
			if cfg.frameOptions != "" {
				h.Set("X-Frame-Options", cfg.frameOptions)
			}
			if cfg.contentTypeNosniff {
				h.Set("X-Content-Type-Options", "nosniff")
			}
			if cfg.referrerPolicy != "" {
				h.Set("Referrer-Policy", cfg.referrerPolicy)
			}
			if cfg.contentSecurityPolicy != "" {
				h.Set("Content-Security-Policy", cfg.contentSecurityPolicy)
			}
			if cfg.permissionsPolicy != "" {
				h.Set("Permissions-Policy", cfg.permissionsPolicy)
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// hstsHeaderValue returns the Strict-Transport-Security value, or "" when HSTS
// is disabled.
func (c *config) hstsHeaderValue() string {
	if c.hstsMaxAge <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("max-age=")
	b.WriteString(strconv.FormatInt(int64(c.hstsMaxAge.Seconds()), 10))
	if c.hstsIncludeSubdomains {
		b.WriteString("; includeSubDomains")
	}
	if c.hstsPreload {
		b.WriteString("; preload")
	}
	return b.String()
}

// isSecure reports whether the request is served over a secure transport,
// honouring X-Forwarded-Proto when configured to trust it.
func (c *config) isSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if c.trustForwardedProto && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}
