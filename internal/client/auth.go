package client

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// EntraIDScope is the OAuth2 scope for the IoT Hub service API (public cloud).
const EntraIDScope = "https://iothubs.azure.net/.default"

// SharedAccessKey is a hub shared access policy (SAS authentication).
type SharedAccessKey struct {
	// HostName of the hub the policy belongs to; SAS tokens are only valid
	// for this hub.
	HostName string
	// KeyName is the policy name (e.g. iothubowner, service, registryReadWrite).
	KeyName string
	// Key is the base64-encoded policy key.
	Key string
}

// SASToken mints an IoT Hub shared access signature for resourceURI (a hub
// hostname, or "<host>/devices/<id>" for a device token) that expires at
// expiry. Format: SharedAccessSignature sr=<uri>&sig=<sig>&se=<unix>[&skn=<policy>].
func SASToken(resourceURI, keyName, key string, expiry time.Time) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return "", fmt.Errorf("shared access key is not valid base64: %w", err)
	}
	sr := url.QueryEscape(resourceURI)
	se := expiry.Unix()
	mac := hmac.New(sha256.New, raw)
	fmt.Fprintf(mac, "%s\n%d", sr, se)
	sig := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	tok := fmt.Sprintf("SharedAccessSignature sr=%s&sig=%s&se=%d", sr, sig, se)
	if keyName != "" {
		tok += "&skn=" + url.QueryEscape(keyName)
	}
	return tok, nil
}

// sasPolicy sets the Authorization header from a cached SAS token that is
// refreshed at 75% of its lifetime. Safe for concurrent use.
type sasPolicy struct {
	key SharedAccessKey
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

func newSASPolicy(key SharedAccessKey, ttl time.Duration) (*sasPolicy, error) {
	if key.HostName == "" || key.KeyName == "" || key.Key == "" {
		return nil, fmt.Errorf("shared access key needs HostName, KeyName and Key")
	}
	if _, err := base64.StdEncoding.DecodeString(key.Key); err != nil {
		return nil, fmt.Errorf("shared access key is not valid base64: %w", err)
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &sasPolicy{key: key, ttl: ttl, now: time.Now}, nil
}

func (p *sasPolicy) current() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if p.token != "" && now.Before(p.expires.Add(-p.ttl/4)) {
		return p.token, nil
	}
	exp := now.Add(p.ttl)
	tok, err := SASToken(p.key.HostName, p.key.KeyName, p.key.Key, exp)
	if err != nil {
		return "", err
	}
	p.token, p.expires = tok, exp
	return tok, nil
}

func (p *sasPolicy) Do(req *policy.Request) (*http.Response, error) {
	tok, err := p.current()
	if err != nil {
		return nil, err
	}
	req.Raw().Header.Set("Authorization", tok)
	return req.Next()
}
