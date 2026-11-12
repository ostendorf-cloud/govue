package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ostendorf-cloud/web-kit/clientip"
	"github.com/ostendorf-cloud/web-kit/ratelimit"
)

func TestRateLimitAllowsThenDeniesWithDefaultResponder(t *testing.T) {
	limiter := ratelimit.New(1, time.Minute, 10)
	limiter.Now = func() time.Time { return time.Unix(0, 0) }

	reached := 0
	handler := RateLimit{Limiter: limiter}.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached++
	}))

	req := func() *http.Request {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		r.RemoteAddr = "192.0.2.1:1234"
		return r
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req())
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req())
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", second.Code)
	}
	if reached != 1 {
		t.Errorf("handler reached %d times, want 1", reached)
	}
	if got := second.Header().Get("Retry-After"); got == "" {
		t.Error("no Retry-After header on the denied response")
	}
}

func TestRateLimitRespondOverrideReplacesTheDefault(t *testing.T) {
	limiter := ratelimit.New(0, time.Minute, 10)

	var gotWait time.Duration
	handler := RateLimit{
		Limiter: limiter,
		Respond: func(w http.ResponseWriter, _ *http.Request, retryAfter time.Duration) {
			gotWait = retryAfter
			w.Header().Set("X-Custom-Denied", "yes")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("slow down"))
		},
	}.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the handler must not run when the limiter denies the request")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 from the override", rec.Code)
	}
	if rec.Header().Get("X-Custom-Denied") != "yes" {
		t.Error("the override's header was not set")
	}
	if rec.Body.String() != "slow down" {
		t.Errorf("body = %q, want the override's body", rec.Body.String())
	}
	if gotWait <= 0 {
		t.Error("the override did not receive the limiter's wait")
	}
}

func TestRateLimitGivesEachClientItsOwnBucket(t *testing.T) {
	limiter := ratelimit.New(1, time.Minute, 10)
	limiter.Now = func() time.Time { return time.Unix(0, 0) }

	handler := RateLimit{
		Limiter:  limiter,
		ClientIP: clientip.NewResolver(nil),
	}.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	requestFrom := func(addr string) int {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec.Code
	}

	if got := requestFrom("192.0.2.1:1111"); got != http.StatusOK {
		t.Errorf("first client's first request = %d, want 200", got)
	}
	if got := requestFrom("192.0.2.2:2222"); got != http.StatusOK {
		t.Errorf("second client's first request = %d, want 200", got)
	}
	if got := requestFrom("192.0.2.1:1111"); got != http.StatusTooManyRequests {
		t.Errorf("first client's second request = %d, want 429", got)
	}
	if got := requestFrom("192.0.2.2:2222"); got != http.StatusTooManyRequests {
		t.Errorf("second client's second request = %d, want 429", got)
	}
}

func TestRateLimitWithoutClientIPSharesOneBucket(t *testing.T) {
	limiter := ratelimit.New(1, time.Minute, 10)
	limiter.Now = func() time.Time { return time.Unix(0, 0) }

	handler := RateLimit{Limiter: limiter}.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	requestFrom := func(addr string) int {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec.Code
	}

	if got := requestFrom("192.0.2.1:1111"); got != http.StatusOK {
		t.Errorf("first client's request = %d, want 200", got)
	}
	if got := requestFrom("192.0.2.2:2222"); got != http.StatusTooManyRequests {
		t.Errorf("a different client without a ClientIP resolver = %d, want 429 from the shared bucket", got)
	}
}
