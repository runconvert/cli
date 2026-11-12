package auth

import (
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func isolate(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)
	t.Setenv("RUNCONVERT_API_KEY", "")
}

func TestKeychainRoundTrip(t *testing.T) {
	isolate(t)
	keyring.MockInit()
	where, err := SaveKey("rc_live_abc")
	if err != nil {
		t.Fatal(err)
	}
	if where != "the system keychain" {
		t.Fatalf("saved to %q, want the keychain", where)
	}
	p, _ := Path()
	b, _ := os.ReadFile(p)
	if string(b) == "" || strings.Contains(string(b), "rc_live_abc") {
		t.Fatalf("the config file must not hold the key when the keychain works: %s", b)
	}
	if k := Key(""); k != "rc_live_abc" {
		t.Fatalf("Key() = %q", k)
	}
	if k := Key("rc_live_flag"); k != "rc_live_flag" {
		t.Fatalf("--key must win, got %q", k)
	}
	if err := Remove(); err != nil {
		t.Fatal(err)
	}
	if k := Key(""); k != "" {
		t.Fatalf("after Remove, Key() = %q", k)
	}
}

func TestFileFallback(t *testing.T) {
	isolate(t)
	keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)
	where, err := SaveKey("rc_live_file")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := Path()
	if where != p {
		t.Fatalf("saved to %q, want %q", where, p)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode %v, want 0600", st.Mode().Perm())
	}
	if k := Key(""); k != "rc_live_file" {
		t.Fatalf("Key() = %q", k)
	}
}
