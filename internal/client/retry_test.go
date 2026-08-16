package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// newTestClient builds a bearer-mode client against srv with a fake sleeper
// that records delays and never actually waits, and jitter fixed at 1.0.
func newTestClient(t *testing.T, srv *httptest.Server, tune func(*RetryOptions)) (*Client, *[]time.Duration) {
	t.Helper()
	var delays []time.Duration
	opts := RetryOptions{
		Rand:  func() float64 { return 1 },
		Sleep: func(ctx context.Context, d time.Duration) error { delays = append(delays, d); return ctx.Err() },
	}
	if tune != nil {
		tune(&opts)
	}
	c, err := New(Config{Hostname: "hub.azure-devices.net", Credential: fakeCred{}, Transport: redirectTo(srv), Retry: opts})
	if err != nil {
		t.Fatal(err)
	}
	return c, &delays
}

func TestRetry_ThrottleThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if r.Header.Get("Authorization") != "Bearer fake-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch n {
		case 1:
			w.Header().Set("iothub-errorcode", "ThrottlingBacklogTimeout")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"Message":"ErrorCode:ThrottlingBacklogTimeout;The request has been throttled. Wait 10 seconds and try again. Operation type: RegistryRead"}`))
		case 2:
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(503)
		default:
			_, _ = w.Write([]byte(`{"totalDeviceCount":9,"enabledDeviceCount":8,"disabledDeviceCount":1}`))
		}
	}))
	defer srv.Close()
	c, delays := newTestClient(t, srv, nil)
	st, err := c.GetRegistryStatistics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.TotalDeviceCount != 9 || atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("stats %+v after %d calls", st, calls)
	}
	// 1st: no Retry-After -> throttle base (10s, jitter 1.0); 2nd: Retry-After 3s.
	if len(*delays) != 2 || (*delays)[0] != 10*time.Second || (*delays)[1] != 3*time.Second {
		t.Fatalf("delays = %v", *delays)
	}
}

func TestRetry_TransientGivesUpAfterMaxAttempts(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"Message":"ErrorCode:GenericServerError;boom"}`))
	}))
	defer srv.Close()
	c, delays := newTestClient(t, srv, func(o *RetryOptions) { o.MaxTransientAttempts = 2 })
	_, err := c.GetRegistryStatistics(context.Background())
	e, ok := AsError(err)
	if !ok || e.StatusCode != 500 || e.Code != "GenericServerError" {
		t.Fatalf("expected *Error 500 GenericServerError, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 3 { // 1 + 2 retries
		t.Fatalf("calls = %d, want 3", calls)
	}
	// exponential from TransientBaseDelay (1s): 1s, 2s
	if len(*delays) != 2 || (*delays)[0] != time.Second || (*delays)[1] != 2*time.Second {
		t.Fatalf("delays = %v", *delays)
	}
}

func TestRetry_ThrottleUntilDeadline(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("iothub-errorcode", "ThrottlingBacklogTimeout")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"Message":"ErrorCode:ThrottlingBacklogTimeout;The request has been throttled. Operation type: ConfigurationWrite"}`))
	}))
	defer srv.Close()
	// A sleeper that "advances" a fake clock: cancel the context after 3 sleeps.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, delays := newTestClient(t, srv, func(o *RetryOptions) {
		o.MaxTransientAttempts = 1
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			if atomic.LoadInt32(&calls) >= 3 {
				cancel()
			}
			return ctx.Err()
		}
	})
	_ = delays
	_, err := c.GetRegistryStatistics(ctx)
	e, ok := AsError(err)
	if !ok || e.StatusCode != 429 || e.Operation != "ConfigurationWrite" {
		t.Fatalf("expected the last 429 *Error with operation, got %v", err)
	}
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "giving up after") {
		t.Fatalf("error must wrap the context error and say it gave up: %v", err)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("calls = %d, want 3 (throttling is retried until the deadline, not a count)", calls)
	}
}

func TestRetry_NonRetryableStatusIsImmediate(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("iothub-errorcode", "DeviceNotFound")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"Message":"ErrorCode:DeviceNotFound;nope"}`))
	}))
	defer srv.Close()
	c, delays := newTestClient(t, srv, nil)
	_, err := c.GetRegistryStatistics(context.Background())
	if !IsNotFound(err) || atomic.LoadInt32(&calls) != 1 || len(*delays) != 0 {
		t.Fatalf("404 must not be retried: err=%v calls=%d delays=%v", err, calls, *delays)
	}
}

func TestRetry_TransportErrorThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			// hijack and close to produce a transport error
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"connectedDeviceCount":1}`))
	}))
	defer srv.Close()
	c, delays := newTestClient(t, srv, nil)
	st, err := c.GetServiceStatistics(context.Background())
	if err != nil || st.ConnectedDeviceCount != 1 {
		t.Fatalf("expected success after one transport error: %v %+v", err, st)
	}
	if len(*delays) != 1 || (*delays)[0] != time.Second {
		t.Fatalf("delays = %v", *delays)
	}
}

// failingCred is a credential that cannot get a token, as an expired login or
// a wrong secret would be. azidentity wraps such failures as non-retriable.
type failingCred struct{ calls int32 }

func (f *failingCred) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	atomic.AddInt32(&f.calls, 1)
	return azcore.AccessToken{}, &azidentity.AuthenticationFailedError{}
}

func TestRetry_CredentialFailureIsNotRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	cred := &failingCred{}
	var delays []time.Duration
	c, err := New(Config{Hostname: "hub.azure-devices.net", Credential: cred, Transport: redirectTo(srv), Retry: RetryOptions{
		Sleep: func(ctx context.Context, d time.Duration) error { delays = append(delays, d); return ctx.Err() },
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetServiceStatistics(context.Background()); err == nil {
		t.Fatal("expected the credential error")
	}
	if n := atomic.LoadInt32(&cred.calls); n != 1 || len(delays) != 0 {
		t.Fatalf("a credential failure must surface immediately: %d token requests, delays %v", n, delays)
	}
}

func TestBackoff_Bounds(t *testing.T) {
	p := &retryPolicy{opts: RetryOptions{Rand: func() float64 { return 0 }}.withDefaults()}
	if d := p.backoff(10*time.Second, 0); d != 5*time.Second {
		t.Errorf("min jitter must be half the base, got %v", d)
	}
	p.opts.Rand = func() float64 { return 1 }
	if d := p.backoff(10*time.Second, 10); d != 60*time.Second {
		t.Errorf("must cap at MaxDelay, got %v", d)
	}
	if d := p.backoff(time.Second, 3); d != 8*time.Second {
		t.Errorf("exponential: got %v", d)
	}
}

func TestRetryableError_DNSNotFound(t *testing.T) {
	if retryableError(&net.DNSError{Err: "no such host", Name: "nope.azure-devices.net", IsNotFound: true}) {
		t.Error("an unresolvable hostname must not be retried")
	}
	if !retryableError(&net.DNSError{Err: "timeout", Name: "x", IsTimeout: true}) {
		t.Error("a DNS timeout is transient and should be retried")
	}
	if retryableError(context.Canceled) {
		t.Error("caller cancellation must not be retried")
	}
}
