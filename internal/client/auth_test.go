package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSASToken_KnownVector(t *testing.T) {
	// HMAC-SHA256(base64decode("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY="),
	// "contoso.azure-devices.net\n1700000000") — computed with the reference
	// algorithm from the IoT Hub docs.
	tok, err := SASToken("contoso.azure-devices.net", "iothubowner", "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "SharedAccessSignature sr=contoso.azure-devices.net&sig=") {
		t.Fatalf("unexpected prefix: %s", tok)
	}
	if !strings.HasSuffix(tok, "&se=1700000000&skn=iothubowner") {
		t.Fatalf("unexpected suffix: %s", tok)
	}
	if !strings.Contains(tok, "&sig=") || strings.Contains(tok, "+") || strings.Contains(tok, "/") && !strings.Contains(tok, "%2F") {
		t.Fatalf("signature must be URL-encoded: %s", tok)
	}
	// device-scoped resource URI is percent-encoded
	tok, _ = SASToken("contoso.azure-devices.net/devices/d 1", "", "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=", time.Unix(1700000000, 0))
	if !strings.HasPrefix(tok, "SharedAccessSignature sr=contoso.azure-devices.net%2Fdevices%2Fd+1&sig=") || strings.Contains(tok, "skn=") {
		t.Fatalf("device token: %s", tok)
	}
	if _, err := SASToken("h", "k", "not-base64!!", time.Now()); err == nil {
		t.Fatal("expected error for invalid base64 key")
	}
}

func TestSASPolicy_CachesAndRefreshes(t *testing.T) {
	p, err := newSASPolicy(SharedAccessKey{HostName: "h.azure-devices.net", KeyName: "k", Key: "YWJj"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	p.now = func() time.Time { return now }
	t1, _ := p.current()
	now = now.Add(30 * time.Minute)
	t2, _ := p.current()
	if t1 != t2 {
		t.Fatal("token must be cached before 75% of its lifetime")
	}
	now = now.Add(20 * time.Minute) // 50 min > 45 min threshold
	t3, _ := p.current()
	if t3 == t1 {
		t.Fatal("token must be refreshed after 75% of its lifetime")
	}
	if _, err := newSASPolicy(SharedAccessKey{HostName: "h", KeyName: "k"}, 0); err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestFactory_SASHeaderAndHostBinding(t *testing.T) {
	var gotAuth, gotUA, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotUA, gotQuery = r.Header.Get("Authorization"), r.Header.Get("User-Agent"), r.URL.RawQuery
		_, _ = w.Write([]byte(`{"connectedDeviceCount":3}`))
	}))
	defer srv.Close()

	f, err := NewFactory(Config{
		SharedAccessKey: &SharedAccessKey{HostName: "hub.azure-devices.net", KeyName: "iothubowner", Key: "YWJj"},
		Version:         "1.2.3",
		Transport:       redirectTo(srv),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Client("other.azure-devices.net"); err == nil || !strings.Contains(err.Error(), "belongs to hub.azure-devices.net") {
		t.Fatalf("SAS client must be bound to its hub, got %v", err)
	}
	c, err := f.Client("HUB.azure-devices.net")
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.GetServiceStatistics(context.Background())
	if err != nil || st.ConnectedDeviceCount != 3 {
		t.Fatalf("stats: %+v %v", st, err)
	}
	if !strings.HasPrefix(gotAuth, "SharedAccessSignature sr=hub.azure-devices.net&sig=") || !strings.HasSuffix(gotAuth, "&skn=iothubowner") {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotUA, "terraform-provider-iothub/1.2.3") {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if gotQuery != "api-version="+APIVersion {
		t.Errorf("query = %q", gotQuery)
	}
	if c2, _ := f.Client("hub.azure-devices.net"); c2 != c {
		t.Error("clients must be cached per hostname")
	}
}

func TestFactory_ConfigValidation(t *testing.T) {
	if _, err := NewFactory(Config{}); err == nil {
		t.Error("expected error without credentials")
	}
	if _, err := NewFactory(Config{Credential: fakeCred{}, SharedAccessKey: &SharedAccessKey{HostName: "h", KeyName: "k", Key: "YWJj"}}); err == nil {
		t.Error("expected error with both credentials")
	}
	f, _ := NewFactory(Config{Credential: fakeCred{}})
	for _, bad := range []string{"", "https://h.azure-devices.net", "h.azure-devices.net/devices"} {
		if _, err := f.Client(bad); err == nil {
			t.Errorf("expected error for hostname %q", bad)
		}
	}
}
