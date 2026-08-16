package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// RetryOptions tune the throttle-aware retry policy. Zero values select the
// defaults described in CONCEPT.md §11.2: retries are bounded by the request
// context's deadline (the Terraform operation timeout), not by a count.
type RetryOptions struct {
	// ThrottleBaseDelay is the first back-off after a 429/503 when the
	// service sends no Retry-After. IoT Hub says "wait 10 seconds" in its
	// throttling message. Default 10s.
	ThrottleBaseDelay time.Duration
	// TransientBaseDelay is the first back-off after 500/502/504/408 or a
	// network error. Default 1s.
	TransientBaseDelay time.Duration
	// MaxDelay caps a single back-off. Default 60s.
	MaxDelay time.Duration
	// TryTimeout bounds one attempt; IoT Hub's traffic shaping can queue a
	// request for seconds before answering, so this stays generous.
	// Default 90s.
	TryTimeout time.Duration
	// MaxTransientAttempts caps retries of non-throttling failures (a 500
	// that repeats is not going to fix itself). Throttling is retried until
	// the deadline. Default 6.
	MaxTransientAttempts int
	// MaxElapsed is the safety net when the request context has no deadline.
	// Default 20m.
	MaxElapsed time.Duration
	// Rand overrides the jitter source (tests).
	Rand func() float64
	// Sleep overrides the back-off sleep (tests). It must honour ctx.
	Sleep func(ctx context.Context, d time.Duration) error
}

func (o RetryOptions) withDefaults() RetryOptions {
	if o.ThrottleBaseDelay <= 0 {
		o.ThrottleBaseDelay = 10 * time.Second
	}
	if o.TransientBaseDelay <= 0 {
		o.TransientBaseDelay = time.Second
	}
	if o.MaxDelay <= 0 {
		o.MaxDelay = 60 * time.Second
	}
	if o.TryTimeout <= 0 {
		o.TryTimeout = 90 * time.Second
	}
	if o.MaxTransientAttempts <= 0 {
		o.MaxTransientAttempts = 6
	}
	if o.MaxElapsed <= 0 {
		o.MaxElapsed = 20 * time.Minute
	}
	if o.Rand == nil {
		o.Rand = rand.Float64
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	return o
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Logger receives debug messages from the client (typically tflog.Debug).
type Logger func(ctx context.Context, msg string, fields map[string]any)

// retryPolicy implements CONCEPT.md §11.2: 429 and 503 are retried until the
// context deadline (Retry-After honoured when present, otherwise exponential
// back-off with full jitter from ThrottleBaseDelay); 500/502/504/408 and
// network errors are retried a bounded number of times with a shorter
// back-off. Every attempt gets its own TryTimeout.
type retryPolicy struct {
	opts RetryOptions
	log  Logger
}

// perRequest tunes the retry policy for one call (carried in the request
// context): non-idempotent operations such as direct methods and job
// creation must not be re-sent after an ambiguous failure, and calls that
// legitimately take long (a direct method waiting for a device response)
// need a longer per-try timeout.
type perRequest struct {
	// OnlyThrottleRetries limits retries to 429 (the request was not
	// processed) — no retry on 5xx/408/network errors.
	OnlyThrottleRetries bool
	// TryTimeout overrides RetryOptions.TryTimeout when > 0.
	TryTimeout time.Duration
}

type perRequestKey struct{}

// withPerRequest attaches per-call retry tuning to ctx.
func withPerRequest(ctx context.Context, pr perRequest) context.Context {
	return context.WithValue(ctx, perRequestKey{}, pr)
}

func perRequestFrom(ctx context.Context) perRequest {
	pr, _ := ctx.Value(perRequestKey{}).(perRequest)
	return pr
}

func (p *retryPolicy) Do(req *policy.Request) (*http.Response, error) {
	ctx := req.Raw().Context()
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.opts.MaxElapsed)
		defer cancel()
	}
	pr := perRequestFrom(ctx)
	tryTimeout := p.opts.TryTimeout
	if pr.TryTimeout > 0 {
		tryTimeout = pr.TryTimeout
	}

	var (
		throttled, transient int
		lastErr              error
	)
	for attempt := 1; ; attempt++ {
		if attempt > 1 {
			if err := req.RewindBody(); err != nil {
				return nil, err
			}
		}
		tryCtx, cancel := context.WithTimeout(ctx, tryTimeout)
		resp, err := req.Clone(tryCtx).Next()

		// ---- decide ----------------------------------------------------
		retry, throttle := false, false
		switch {
		case err != nil:
			retry = retryableError(err) && ctx.Err() == nil && !pr.OnlyThrottleRetries
		case retryableStatus(resp.StatusCode):
			retry = ctx.Err() == nil
			throttle = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable
			if pr.OnlyThrottleRetries && resp.StatusCode != http.StatusTooManyRequests {
				retry = false
			}
		}
		var delay time.Duration
		if retry {
			if throttle {
				if err == nil {
					delay = retryAfter(resp)
				}
				if delay <= 0 {
					delay = p.backoff(p.opts.ThrottleBaseDelay, throttled)
				}
				throttled++
			} else {
				transient++
				if transient > p.opts.MaxTransientAttempts {
					retry = false
				} else {
					delay = p.backoff(p.opts.TransientBaseDelay, transient-1)
				}
			}
		}

		// ---- return without retry ---------------------------------------
		if !retry {
			if err != nil {
				cancel()
				if lastErr != nil && ctx.Err() != nil {
					return nil, fmt.Errorf("%w (giving up after %d attempts: %w)", lastErr, attempt-1, ctx.Err())
				}
				return nil, err
			}
			// The body must outlive tryCtx: release it when the caller closes.
			resp.Body = &bodyWithCancel{ReadCloser: resp.Body, cancel: cancel}
			return resp, nil
		}

		// ---- retry ---------------------------------------------------------
		if err == nil {
			body, _ := runtime.Payload(resp) // reads and closes the body
			lastErr = newError(resp, body)
			p.logf(ctx, "retrying IoT Hub request after HTTP status", map[string]any{
				"status": resp.StatusCode, "errorcode": resp.Header.Get("iothub-errorcode"),
				"delay": delay.String(), "attempt": attempt,
			})
		} else {
			lastErr = err
			p.logf(ctx, "retrying IoT Hub request after transport error", map[string]any{
				"error": err.Error(), "delay": delay.String(), "attempt": attempt,
			})
		}
		cancel()
		if serr := p.opts.Sleep(ctx, delay); serr != nil {
			return nil, fmt.Errorf("%w (giving up after %d attempts: %w)", lastErr, attempt, serr)
		}
	}
}

func (p *retryPolicy) logf(ctx context.Context, msg string, fields map[string]any) {
	if p.log != nil {
		p.log(ctx, msg, fields)
	}
}

func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryableError reports whether a transport error is worth retrying: network
// errors and per-try timeouts are; a cancellation from the caller, a hostname
// that does not resolve (a typo, or a hub that does not exist) and anything
// azcore marks non-retriable — above all a credential that cannot get a
// token — are not.
func retryableError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var nonRetriable interface{ NonRetriable() }
	if errors.As(err, &nonRetriable) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	// A per-try timeout surfaces as *url.Error (a net.Error) and is handled
	// above; a bare DeadlineExceeded means the parent context expired.
	return !errors.Is(err, context.DeadlineExceeded)
}

// backoff is exponential with full jitter: uniform in [base/2, min(MaxDelay, base*2^n)].
func (p *retryPolicy) backoff(base time.Duration, n int) time.Duration {
	ceil := math.Min(float64(base)*math.Pow(2, float64(n)), float64(p.opts.MaxDelay))
	d := time.Duration(ceil * p.opts.Rand())
	if d < base/2 {
		d = base / 2
	}
	return d
}

// retryAfter parses a Retry-After header (seconds or HTTP-date); 0 if absent.
func retryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// bodyWithCancel releases the per-try context when the caller closes the body.
type bodyWithCancel struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *bodyWithCancel) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
