package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorsCarryCodeAndHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer rc_live_ok" {
			w.WriteHeader(401)
			w.Write([]byte(`{"success":false,"error":{"code":"AUTH_INVALID_TOKEN","message":"Invalid or expired token."}}`))
			return
		}
		w.Write([]byte(`{"success":true,"data":[]}`))
	}))
	defer srv.Close()
	t.Setenv("RUNCONVERT_API_URL", srv.URL)

	_, err := New("rc_live_bad", "t").ListJobs(context.Background(), "", 1)
	e, ok := IsAPIError(err)
	if !ok || e.Status != 401 || e.Code != "AUTH_INVALID_TOKEN" || e.Hint() == "" {
		t.Fatalf("got %#v", err)
	}
	if _, err := New("rc_live_ok", "t").ListJobs(context.Background(), "", 1); err != nil {
		t.Fatal(err)
	}
}

func TestPublicCallsSendNoKey(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write([]byte(`{"success":true,"data":{"format":"png","category":"image","outputs":[]}}`))
	}))
	defer srv.Close()
	t.Setenv("RUNCONVERT_API_URL", srv.URL)
	if _, err := New("rc_live_x", "t").Outputs(context.Background(), "png"); err != nil || auth != "" {
		t.Fatalf("format lookups must not send the key (got %q, %v)", auth, err)
	}
}

func TestDeviceStartMeAndCredits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/oauth/device":
			r.ParseForm()
			if r.Form.Get("client_id") != ClientID || r.Form.Get("device_name") != "Mac" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_request","error_description":"bad"}`))
				return
			}
			w.Write([]byte(`{"device_code":"rcd_1","user_code":"WXYZ-BCDF","verification_uri":"https://w/cli/connect","verification_uri_complete":"https://w/cli/connect?code=WXYZ-BCDF","expires_in":600,"interval":5}`))
		case "/v3/me":
			w.Write([]byte(`{"success":true,"data":{"user":{"email":"d@x.com"},"team":{"name":"RC"},"credits":{"mode":"wallet","total_remaining":1240}}}`))
		}
	}))
	defer srv.Close()
	t.Setenv("RUNCONVERT_API_URL", srv.URL)
	c := New("rc_live_x", "t")
	d, err := c.StartDevice(context.Background(), "Mac")
	if err != nil || d.UserCode != "WXYZ-BCDF" || d.Interval != 5 {
		t.Fatalf("device: %+v %v", d, err)
	}
	if _, err := c.StartDevice(context.Background(), "Other"); err == nil || err.Error() != "bad" {
		t.Fatalf("OAuth errors carry their description: %v", err)
	}
	me, err := c.Me(context.Background())
	if err != nil || me.User.Email != "d@x.com" || me.CreditsText() != "1,240 credits" {
		t.Fatalf("me: %+v %v", me, err)
	}
	me.Credits.Mode, me.Credits.DailyRemaining, me.Credits.DailyLimit = "daily", 12, 20
	if me.CreditsText() != "12 of 20 free credits left today" {
		t.Fatalf("daily: %q", me.CreditsText())
	}
}
