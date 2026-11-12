package auth

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/runconvert/cli/internal/api"
)

// DeviceName is what the website shows and the key is named after:
// "Dinusha's MacBook Air" on a Mac, else the host name.
func DeviceName() string {
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("scutil", "--get", "ComputerName").Output(); err == nil {
			if n := strings.TrimSpace(string(out)); n != "" {
				return n
			}
		}
	}
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "a computer"
	}
	return strings.TrimSuffix(h, ".local")
}

// OpenBrowser opens a URL in the default browser; false where it can't (SSH,
// no desktop) or shouldn't (RUNCONVERT_NO_BROWSER=1).
func OpenBrowser(url string) bool {
	if os.Getenv("RUNCONVERT_NO_BROWSER") == "1" || os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return false
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start() == nil
}

// ErrDenied: the person pressed Cancel on the website.
var ErrDenied = errors.New("sign-in was cancelled on the website")

// ErrExpired: nobody approved the code in time.
var ErrExpired = errors.New("the code expired; run `runconvert login` again")

// WaitForApproval checks every few seconds (as the API asks) until the code
// is approved, cancelled or expired, and returns the API key.
func WaitForApproval(ctx context.Context, c *api.Client, d *api.DeviceCode) (string, error) {
	interval := time.Duration(max(d.Interval, 1)) * time.Second
	deadline := time.Now().Add(time.Duration(max(d.ExpiresIn, 60)) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		key, err := c.DeviceToken(ctx, d.DeviceCode)
		var oe *api.OAuthError
		switch {
		case err == nil:
			return key, nil
		case errors.As(err, &oe) && oe.Code == "authorization_pending":
		case errors.As(err, &oe) && oe.Code == "slow_down":
			interval += 5 * time.Second
		case errors.As(err, &oe) && oe.Code == "access_denied":
			return "", ErrDenied
		case errors.As(err, &oe) && oe.Code == "expired_token":
			return "", ErrExpired
		default:
			return "", err
		}
		if time.Now().After(deadline) {
			return "", ErrExpired
		}
	}
}
