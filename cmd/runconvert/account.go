package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/runconvert/runconvert-cli/internal/api"
	"github.com/runconvert/runconvert-cli/internal/auth"
)

func loginCmd() *cobra.Command {
	var noBrowser, withKey bool
	cmd := &cobra.Command{
		Use:   "login [api-key]",
		Short: "Sign in through runconvert.com (or with an API key)",
		Long: "Opens runconvert.com in your browser: sign in, check the code matches, and click\n" +
			"Authorize. The terminal is then signed in with a key named \"CLI · <this computer>\",\n" +
			"saved in the system keychain. Revoke it any time in Dashboard → API keys.\n\n" +
			"On servers and over SSH, --no-browser prints the link to open on any device.\n" +
			"In CI, set RUNCONVERT_API_KEY instead of logging in.",
		Example: "  runconvert login                   # opens the browser\n" +
			"  runconvert login --no-browser      # prints a link and a code\n" +
			"  runconvert login --with-key        # paste an API key instead\n" +
			"  runconvert login rc_live_xxxxxxxx",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			key := keyFlag
			if len(args) == 1 {
				key = args[0]
			}
			if key == "" && withKey {
				fmt.Fprint(os.Stderr, "Paste your API key (https://www.runconvert.com/dashboard → API keys): ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				key = strings.TrimSpace(line)
			}
			if key == "" {
				var err error
				if key, err = browserLogin(ctx, !noBrowser); err != nil {
					return err
				}
			} else if !strings.HasPrefix(key, "rc_live_") {
				return errors.New("that isn't a RunConvert API key (they start with rc_live_)")
			}
			c := api.New(key, userAgent())
			me, err := c.Me(ctx)
			if e, ok := api.IsAPIError(err); ok && e.Status == 404 {
				// An API without /v3/me: check the key another way.
				me, err = nil, nil
				if _, err = c.ListJobs(ctx, "", 1); err != nil {
					return err
				}
			}
			if err != nil {
				return err
			}
			where, err := auth.SaveKey(key)
			if err != nil {
				return err
			}
			if me != nil {
				fmt.Printf("✓ Signed in as %s · %s", me.User.Email, me.Team.Name)
				if t := me.CreditsText(); t != "" {
					fmt.Printf(" · %s", t)
				}
			} else {
				fmt.Print("✓ Signed in")
			}
			fmt.Printf("\n  Key saved in %s. Now convert something great: runconvert\n", where)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the link instead of opening a browser (servers, SSH)")
	cmd.Flags().BoolVar(&withKey, "with-key", false, "paste an API key instead of signing in through the browser")
	return cmd
}

// browserLogin shows a code, opens runconvert.com/cli/connect and waits for
// Authorize there.
func browserLogin(ctx context.Context, openBrowser bool) (string, error) {
	c := api.New("", userAgent())
	d, err := c.StartDevice(ctx, auth.DeviceName())
	if e, ok := api.IsAPIError(err); ok && e.Status == 404 {
		return "", errors.New("signing in through the browser isn't available on this server yet. Use `runconvert login --with-key` with a key from https://www.runconvert.com/dashboard")
	}
	if err != nil {
		return "", err
	}
	opened := openBrowser && auth.OpenBrowser(d.VerificationURIComplete)
	if opened {
		fmt.Fprintln(os.Stderr, "Opening runconvert.com in your browser…")
		fmt.Fprintf(os.Stderr, "If it doesn't open, go to %s and enter:\n\n", d.VerificationURI)
	} else {
		fmt.Fprintf(os.Stderr, "Open this link on any device and sign in:\n\n  %s\n\nand check the code is:\n\n", d.VerificationURIComplete)
	}
	fmt.Fprintf(os.Stderr, "    %s\n\n", d.UserCode)

	done := make(chan struct{})
	if isTerminal(os.Stderr) {
		go func() {
			frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
			for i := 0; ; i++ {
				select {
				case <-done:
					fmt.Fprint(os.Stderr, "\r\033[K")
					return
				case <-time.After(100 * time.Millisecond):
					fmt.Fprintf(os.Stderr, "\r%s Waiting for you to authorize… (Ctrl+C to stop)", frames[i%len(frames)])
				}
			}
		}()
	} else {
		fmt.Fprintln(os.Stderr, "Waiting for you to authorize…")
	}
	key, err := auth.WaitForApproval(ctx, c, d)
	close(done)
	time.Sleep(50 * time.Millisecond) // let the spinner clear its line
	return key, err
}

func whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Who you're signed in as, and your credits",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client(true)
			if err != nil {
				return err
			}
			me, err := c.Me(cmd.Context())
			if e, ok := api.IsAPIError(err); ok && e.Status == 404 {
				if _, err := c.ListJobs(cmd.Context(), "", 1); err != nil {
					return err
				}
				fmt.Println("Signed in (this server doesn't share account details yet).")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Printf("%s · %s\n", me.User.Email, me.Team.Name)
			if t := me.CreditsText(); t != "" {
				fmt.Println(t)
			}
			if me.APIKey != nil {
				fmt.Printf("Key: %s\n", me.APIKey.Name)
			}
			return nil
		},
	}
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "logout",
		Short:   "Remove the saved key from this computer",
		Long:    "Removes the key from this computer. To revoke it everywhere, delete it in Dashboard → API keys.",
		Example: "  runconvert logout",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := auth.Remove(); err != nil {
				return err
			}
			fmt.Println("✓ Signed out on this computer.")
			return nil
		},
	}
}
