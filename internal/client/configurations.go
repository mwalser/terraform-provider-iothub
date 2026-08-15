package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// Configuration is an automatic device management configuration or an IoT
// Edge deployment (both live under /configurations/{id}). Content sections
// are kept raw for number-preserving decoding by the provider.
type Configuration struct {
	ID                 string                `json:"id"`
	SchemaVersion      string                `json:"schemaVersion,omitempty"`
	Labels             map[string]string     `json:"labels,omitempty"`
	Content            ConfigurationContent  `json:"content"`
	TargetCondition    string                `json:"targetCondition"`
	CreatedTimeUTC     string                `json:"createdTimeUtc,omitempty"`
	LastUpdatedTimeUTC string                `json:"lastUpdatedTimeUtc,omitempty"`
	Priority           int64                 `json:"priority"`
	SystemMetrics      *ConfigurationMetrics `json:"systemMetrics,omitempty"`
	Metrics            *ConfigurationMetrics `json:"metrics,omitempty"`
	ETag               string                `json:"etag,omitempty"`
}

// ConfigurationContent holds exactly one of the three content shapes:
// deviceContent (device twin desired properties), moduleContent (module
// twin desired properties) or modulesContent (IoT Edge deployment).
type ConfigurationContent struct {
	DeviceContent  json.RawMessage `json:"deviceContent,omitempty"`
	ModuleContent  json.RawMessage `json:"moduleContent,omitempty"`
	ModulesContent json.RawMessage `json:"modulesContent,omitempty"`
}

// ConfigurationMetrics are metric queries and their latest results.
type ConfigurationMetrics struct {
	Results map[string]int64  `json:"results,omitempty"`
	Queries map[string]string `json:"queries,omitempty"`
}

// ConfigurationSpec is what the provider writes. PUT is a full replace and
// the service requires content on every write (400 InvalidConfigurationContent
// otherwise) — content itself is immutable and silently kept if it differs
// (verified), which the provider handles by replacing the resource.
type ConfigurationSpec struct {
	ID              string
	SchemaVersion   string
	Labels          map[string]string // nil: omitted; empty: sent as {}
	Content         ConfigurationContent
	TargetCondition string
	Priority        int64
	Metrics         map[string]string // custom metric queries; nil: omitted
}

func (s ConfigurationSpec) body() map[string]any {
	b := map[string]any{
		"id":              s.ID,
		"content":         s.Content,
		"targetCondition": s.TargetCondition,
		"priority":        s.Priority,
	}
	if s.SchemaVersion != "" {
		b["schemaVersion"] = s.SchemaVersion
	}
	if s.Labels != nil {
		b["labels"] = s.Labels
	}
	if s.Metrics != nil {
		b["metrics"] = map[string]any{"queries": s.Metrics}
	}
	return b
}

func configurationPath(id string) string { return "/configurations/" + id }

// GetConfiguration reads a configuration; missing → IsNotFound.
func (c *Client) GetConfiguration(ctx context.Context, id string) (*Configuration, error) {
	var out Configuration
	if _, err := c.do(ctx, request{method: http.MethodGet, path: configurationPath(id)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListConfigurations returns all configurations and deployments of the hub
// (at most 100 exist per hub; the service ignores ?top).
func (c *Client) ListConfigurations(ctx context.Context) ([]Configuration, error) {
	var out []Configuration
	if _, err := c.do(ctx, request{method: http.MethodGet, path: "/configurations"}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateConfiguration creates a configuration. A PUT without If-Match is
// create-only: an existing ID answers 409 ConfigurationAlreadyExists.
func (c *Client) CreateConfiguration(ctx context.Context, spec ConfigurationSpec) (*Configuration, error) {
	var out Configuration
	if _, err := c.do(ctx, request{method: http.MethodPut, path: configurationPath(spec.ID), body: spec.body()}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateConfiguration replaces the mutable fields. etag must be the value
// from the last read (it is quoted for the wire — the only form the service
// honours for configurations) or "*"; a stale value answers 412.
func (c *Client) UpdateConfiguration(ctx context.Context, spec ConfigurationSpec, etag string) (*Configuration, error) {
	var out Configuration
	if etag == "" {
		etag = "*"
	}
	r := request{method: http.MethodPut, path: configurationPath(spec.ID), body: spec.body(), headers: ifMatch(etag)}
	if _, err := c.do(ctx, r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteConfiguration deletes a configuration; If-Match is optional for
// configurations but "*" is sent for uniformity. Missing → 404.
func (c *Client) DeleteConfiguration(ctx context.Context, id, etag string) error {
	if etag == "" {
		etag = "*"
	}
	_, err := c.do(ctx, request{method: http.MethodDelete, path: configurationPath(id), headers: ifMatch(etag)}, nil)
	return err
}

// TestQueriesResult is the answer of POST /configurations/testQueries: HTTP
// 200 with the errors (if any) in the body.
type TestQueriesResult struct {
	TargetConditionError    string            `json:"targetConditionError,omitempty"`
	CustomMetricQueryErrors map[string]string `json:"customMetricQueryErrors,omitempty"`
}

// OK reports whether both the target condition and every metric query
// compiled.
func (r *TestQueriesResult) OK() bool {
	return r.TargetConditionError == "" && len(r.CustomMetricQueryErrors) == 0
}

// TestConfigurationQueries validates a target condition and custom metric
// queries without creating anything. Note that "*" (all devices) is accepted
// as a target condition by PUT but rejected here (verified) — callers skip
// it.
func (c *Client) TestConfigurationQueries(ctx context.Context, targetCondition string, metrics map[string]string) (*TestQueriesResult, error) {
	body := map[string]any{"targetCondition": targetCondition}
	if len(metrics) > 0 {
		body["customMetricQueries"] = metrics
	}
	var out TestQueriesResult
	if _, err := c.do(ctx, request{method: http.MethodPost, path: "/configurations/testQueries", body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
