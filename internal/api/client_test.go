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
