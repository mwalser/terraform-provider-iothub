package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Error is a failed IoT Hub Service API call. The service uses two body
// envelopes — {"Message":"ErrorCode:<code>;<text>","ExceptionMessage":"Tracking ID:…"}
// and {"errorCode":404103,"message":"…","trackingId":"…"} — and sometimes nests the
// second as a string inside the first's Message. The reliable discriminator is
// the iothub-errorcode response header; both envelopes are parsed for the rest.
type Error struct {
	StatusCode int
	// Code is the symbolic error code (DeviceNotFound, PreconditionFailed,
	// ThrottlingBacklogTimeout, …) from the iothub-errorcode header, or from
	// the body when the header is missing.
	Code string
	// Message is the human-readable text without the "ErrorCode:X;" prefix.
	Message string
	// TrackingID and RequestID identify the call for Microsoft support.
	TrackingID string
	RequestID  string
	// Operation is the throttled operation type ("ConfigurationWrite") when
	// the service names one in a throttling message.
	Operation string
	// SASAuth is true when the client that saw the error authenticates with
	// a shared access policy rather than Entra ID; hints depend on it.
	SASAuth bool
	// Method and URL identify the request.
	Method string
	URL    string
	// Body is the raw response body (for logging).
	Body []byte
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: HTTP %d", e.Method, e.URL, e.StatusCode)
	if e.Code != "" {
		fmt.Fprintf(&b, " %s", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	if e.TrackingID != "" {
		fmt.Fprintf(&b, " (tracking ID %s)", e.TrackingID)
	} else if e.RequestID != "" {
		fmt.Fprintf(&b, " (request ID %s)", e.RequestID)
	}
	return b.String()
}

// AsError returns the *Error inside err, if any.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// IsNotFound reports whether err is a 404 (DeviceNotFound, ModuleNotFound,
// ConfigurationNotFound, JobNotFound, …).
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsConflict reports whether err is a 409 (DeviceAlreadyExists,
// ModuleAlreadyExistsOnDevice, ConfigurationAlreadyExists, JobAlreadyExists).
func IsConflict(err error) bool { return hasStatus(err, http.StatusConflict) }

// IsPreconditionFailed reports whether err is a 412 (ETag mismatch or missing).
func IsPreconditionFailed(err error) bool { return hasStatus(err, http.StatusPreconditionFailed) }

// IsUnauthorized reports whether err is a 401/403.
func IsUnauthorized(err error) bool {
	return hasStatus(err, http.StatusUnauthorized) || hasStatus(err, http.StatusForbidden)
}

func hasStatus(err error, status int) bool {
	e, ok := AsError(err)
	return ok && e.StatusCode == status
}

var (
	reErrorCode = regexp.MustCompile(`^ErrorCode:([A-Za-z0-9_]+);`)
	reOperation = regexp.MustCompile(`Operation type:\s*([A-Za-z0-9_]+)`)
)

// newError builds an *Error from a non-2xx response whose body has already
// been read.
func newError(resp *http.Response, body []byte) *Error {
	e := &Error{
		StatusCode: resp.StatusCode,
		Code:       resp.Header.Get("iothub-errorcode"),
		RequestID:  resp.Header.Get("x-ms-request-id"),
		Body:       body,
	}
	if resp.Request != nil {
		e.Method = resp.Request.Method
		if resp.Request.URL != nil {
			u := *resp.Request.URL
			u.RawQuery = "" // never echo query strings (SAS!) into errors
			e.URL = u.String()
		}
	}
	e.parseBody(body)
	if e.Message == "" {
		e.Message = http.StatusText(resp.StatusCode)
	}
	return e
}

// parseBody understands both service envelopes. encoding/json matches keys
// case-insensitively, so the envelopes are told apart by their exact keys.
func (e *Error) parseBody(body []byte) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return
	}
	if m := reOperation.FindSubmatch(body); m != nil {
		e.Operation = string(m[1])
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &raw); err != nil {
		e.Message = firstLine(string(body))
		return
	}
	str := func(key string) string {
		v, ok := raw[key]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
		return string(v)
	}
	_, hasMessage := raw["Message"]
	_, hasException := raw["ExceptionMessage"]
	switch {
	case hasMessage || hasException:
		// Envelope 1: {"Message":"ErrorCode:X;text","ExceptionMessage":"Tracking ID:…"}
		msg := str("Message")
		if m := reErrorCode.FindStringSubmatch(msg); m != nil {
			if e.Code == "" {
				e.Code = m[1]
			}
			msg = strings.TrimSpace(msg[len(m[0]):])
		}
		if _, after, ok := strings.Cut(str("ExceptionMessage"), "Tracking ID:"); ok {
			e.TrackingID = strings.TrimSpace(after)
		}
		// The text may itself be envelope 2 (direct-method 404s do this).
		if strings.HasPrefix(msg, "{") {
			if nested, ok := parseEnvelope2([]byte(msg)); ok {
				msg = nested.message
				if e.TrackingID == "" {
					e.TrackingID = nested.trackingID
				}
			}
		}
		e.Message = firstLine(msg)
	default:
		if env, ok := parseEnvelope2(body); ok {
			e.Message = firstLine(env.message)
			e.TrackingID = env.trackingID
			if e.Code == "" && env.errorCode != "" {
				e.Code = "Error" + env.errorCode
			}
		} else {
			e.Message = firstLine(string(body))
		}
	}
}

type envelope2 struct{ errorCode, message, trackingID string }

// parseEnvelope2 parses {"errorCode":404103,"message":"…","trackingId":"…"}.
func parseEnvelope2(body []byte) (envelope2, bool) {
	var v struct {
		ErrorCode  json.Number `json:"errorCode"`
		Message    string      `json:"message"`
		TrackingID string      `json:"trackingId"`
	}
	if err := json.Unmarshal(body, &v); err != nil || (v.Message == "" && v.ErrorCode == "") {
		return envelope2{}, false
	}
	return envelope2{errorCode: v.ErrorCode.String(), message: v.Message, trackingID: v.TrackingID}, true
}

// firstLine trims a multi-line service message ("Error: 403 …\nMessage: …")
// to its most useful line.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\nMessage: "); i >= 0 {
		rest := s[i+len("\nMessage: "):]
		if j := strings.IndexByte(rest, '\n'); j >= 0 {
			rest = rest[:j]
		}
		return strings.TrimSpace(rest)
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
