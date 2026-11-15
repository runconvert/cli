package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/runconvert/runconvert-cli/internal/api"
)

// tokenServer answers the token endpoint with the given replies in turn.
func tokenServer(t *testing.T, replies ...string) (*api.Client, *int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/oauth/token" || r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.FormValue("client_id") != api.ClientID {
			t.Errorf("unexpected request %s %v", r.URL.Path, r.Form)
		}
		mu.Lock()
		reply := replies[min(n, len(replies)-1)]
		n++
		mu.Unlock()
		if reply == "ok" {
			w.Write([]byte(`{"access_token":"rc_live_new","token_type":"Bearer","scope":"convert"}`))
			return
		}
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"` + reply + `"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("RUNCONVERT_API_URL", srv.URL)
	return api.New("", "test"), &n
}

func TestWaitForApproval(t *testing.T) {
	d := &api.DeviceCode{DeviceCode: "rcd_x", Interval: 0, ExpiresIn: 60}
	c, n := tokenServer(t, "authorization_pending", "authorization_pending", "ok")
	key, err := WaitForApproval(context.Background(), c, d)
	if err != nil || key != "rc_live_new" || *n != 3 {
		t.Fatalf("key %q err %v after %d checks", key, err, *n)
	}

	c, _ = tokenServer(t, "access_denied")
	if _, err := WaitForApproval(context.Background(), c, d); !errors.Is(err, ErrDenied) {
		t.Fatalf("cancelled: %v", err)
	}
	c, _ = tokenServer(t, "expired_token")
	if _, err := WaitForApproval(context.Background(), c, d); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
	c, _ = tokenServer(t, "invalid_grant")
	if _, err := WaitForApproval(context.Background(), c, d); err == nil {
		t.Fatal("other errors stop waiting")
	}
}

func TestNoBrowserSwitch(t *testing.T) {
	t.Setenv("RUNCONVERT_NO_BROWSER", "1")
	if OpenBrowser("https://example.com") {
		t.Fatal("RUNCONVERT_NO_BROWSER=1 must not open a browser")
	}
	if DeviceName() == "" {
		t.Fatal("a device name is always set")
	}
}
