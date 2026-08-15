// Package client is a thin, framework-free client for the Azure IoT Hub
// Service REST API (api-version 2021-04-12), built on the azcore pipeline.
// It encodes the service behaviour verified in CONCEPT.md Appendix D:
// throttle-aware retries, quoted ETags, the two error envelopes, and the
// create/update semantics of If-Match.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// APIVersion is the IoT Hub Service API version this client is written
// against. It is a compiled-in constant, not a setting (CONCEPT.md §11.6).
const APIVersion = "2021-04-12"

const moduleName = "terraform-provider-iothub"

// Config configures a Factory. Exactly one of Credential (Entra ID) or
// SharedAccessKey (SAS) must be set.
type Config struct {
	// Credential is an Entra ID token credential (azidentity); tokens are
	// requested for EntraIDScope.
	Credential azcore.TokenCredential
	// SharedAccessKey selects SAS authentication for one hub.
	SharedAccessKey *SharedAccessKey
	// Version is the provider version, sent in the User-Agent.
	Version string
	// Transport overrides the HTTP transport (tests). Nil uses the azcore
	// default.
	Transport policy.Transporter
	// Retry tunes the retry policy; zero values are the defaults.
	Retry RetryOptions
	// Logger receives debug messages (retries, requests). May be nil.
	Logger Logger
}

// Factory creates per-hub Clients that share one pipeline and credential.
type Factory struct {
	pipeline runtime.Pipeline
	sasHost  string // non-empty in SAS mode: the only hub the key is valid for
	logger   Logger

	mu      sync.Mutex
	clients map[string]*Client
}

// NewFactory builds the shared pipeline for the given configuration.
func NewFactory(cfg Config) (*Factory, error) {
	if (cfg.Credential == nil) == (cfg.SharedAccessKey == nil) {
		return nil, fmt.Errorf("client: exactly one of Credential or SharedAccessKey must be set")
	}
	f := &Factory{logger: cfg.Logger, clients: map[string]*Client{}}

	var auth policy.Policy
	if cfg.SharedAccessKey != nil {
		p, err := newSASPolicy(*cfg.SharedAccessKey, 0)
		if err != nil {
			return nil, err
		}
		auth = p
		f.sasHost = strings.ToLower(cfg.SharedAccessKey.HostName)
	} else {
		auth = runtime.NewBearerTokenPolicy(cfg.Credential, []string{EntraIDScope}, nil)
	}

	version := cfg.Version
	if version == "" {
		version = "dev"
	}
	plOpts := runtime.PipelineOptions{
		// Our own retry policy replaces azcore's (disabled below): it is
		// deadline-bounded and knows IoT Hub's throttling behaviour.
		PerCall: []policy.Policy{
			&apiVersionPolicy{},
			&retryPolicy{opts: cfg.Retry.withDefaults(), log: cfg.Logger},
		},
		PerRetry: []policy.Policy{auth},
	}
	clOpts := &policy.ClientOptions{
		Retry:     policy.RetryOptions{MaxRetries: -1},
		Telemetry: policy.TelemetryOptions{ApplicationID: moduleName + "/" + version},
		Transport: cfg.Transport,
	}
	f.pipeline = runtime.NewPipeline(moduleName, version, plOpts, clOpts)
	return f, nil
}

// Client returns the client for a hub hostname (e.g. contoso.azure-devices.net).
// In SAS mode only the hub the key belongs to can be addressed.
func (f *Factory) Client(hostname string) (*Client, error) {
	host := strings.ToLower(strings.TrimSpace(hostname))
	if host == "" {
		return nil, fmt.Errorf("client: hub hostname is required")
	}
	if strings.Contains(host, "/") || strings.Contains(host, "://") {
		return nil, fmt.Errorf("client: %q is not a bare hostname", hostname)
	}
	if f.sasHost != "" && host != f.sasHost {
		return nil, fmt.Errorf("client: the shared access policy in connection_string belongs to %s and cannot be used for %s; use Entra ID authentication to address several hubs", f.sasHost, host)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.clients[host]; ok {
		return c, nil
	}
	c := &Client{hostname: host, pipeline: f.pipeline, log: f.logger}
	f.clients[host] = c
	return c, nil
}

// Client talks to one IoT Hub.
type Client struct {
	hostname string
	pipeline runtime.Pipeline
	log      Logger
}

// Hostname returns the hub this client addresses.
func (c *Client) Hostname() string { return c.hostname }

// apiVersionPolicy appends api-version to every request.
type apiVersionPolicy struct{}

func (apiVersionPolicy) Do(req *policy.Request) (*http.Response, error) {
	q := req.Raw().URL.Query()
	q.Set("api-version", APIVersion)
	req.Raw().URL.RawQuery = q.Encode()
	return req.Next()
}

// request describes one API call.
type request struct {
	method  string
	path    string // e.g. /devices/{id}, already escaped
	query   url.Values
	headers http.Header
	body    any // marshalled as JSON when non-nil
	// okStatuses lists the statuses treated as success (default: any 2xx).
	okStatuses []int
}

// result carries what callers need besides the decoded body.
type result struct {
	Status  int
	Headers http.Header
}

// do performs the request and decodes a JSON body into out (if non-nil).
// Non-2xx responses become *Error.
//
//nolint:unparam // the result (ETag, headers) is consumed by the identity operations added next.
func (c *Client) do(ctx context.Context, r request, out any) (*result, error) {
	u := url.URL{Scheme: "https", Host: c.hostname, Path: r.path}
	if len(r.query) > 0 {
		u.RawQuery = r.query.Encode()
	}
	req, err := runtime.NewRequest(ctx, r.method, u.String())
	if err != nil {
		return nil, err
	}
	for k, vs := range r.headers {
		for _, v := range vs {
			req.Raw().Header.Add(k, v)
		}
	}
	if r.body != nil {
		if err := runtime.MarshalAsJSON(req, r.body); err != nil {
			return nil, err
		}
	}
	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if len(r.okStatuses) > 0 {
		ok = false
		for _, s := range r.okStatuses {
			if resp.StatusCode == s {
				ok = true
				break
			}
		}
	}
	if !ok {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, newError(resp, body)
	}
	res := &result{Status: resp.StatusCode, Headers: resp.Header}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("%s %s: reading response: %w", r.method, r.path, err)
		}
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := json.Unmarshal(body, out); err != nil {
				return nil, fmt.Errorf("%s %s: decoding response: %w", r.method, r.path, err)
			}
		}
	}
	return res, nil
}
