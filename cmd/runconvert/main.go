// Command runconvert converts files with RunConvert from the terminal.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/runconvert/runconvert-cli/internal/api"
	"github.com/runconvert/runconvert-cli/internal/auth"
	"github.com/runconvert/runconvert-cli/internal/tui"
)

// version is set at build time: -ldflags "-X main.version=1.0.0".
var version = "dev"

var keyFlag string

func userAgent() string {
	return fmt.Sprintf("runconvert-cli/%s (%s/%s)", version, runtime.GOOS, runtime.GOARCH)
}

// client returns an API client; needKey makes a missing key an error.
func client(needKey bool) (*api.Client, error) {
	key := auth.Key(keyFlag)
	if needKey && key == "" {
		return nil, errors.New("not signed in. Run `runconvert login`, or set RUNCONVERT_API_KEY (keys: https://www.runconvert.com/dashboard)")
	}
	return api.New(key, userAgent()), nil
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := &cobra.Command{
		Use:   "runconvert",
		Short: "Convert files between 3,000+ formats with RunConvert",
		Long: "Convert video, audio, images, documents, ebooks, fonts, CAD and archives with RunConvert.\n\n" +
			"Quick start:\n" +
			"  runconvert login               sign in through runconvert.com\n" +
			"  runconvert                     the app: drag files in, pick a format\n" +
			"  runconvert convert photo.heic --to jpg   one command, for scripts and servers\n" +
			"  runconvert formats             what converts to what\n\n" +
			"More help: runconvert help paths | help options | help env",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&keyFlag, "key", "", "API key (default: RUNCONVERT_API_KEY, then the saved one)")
	root.SuggestionsMinimumDistance = 2
	var noInput bool
	root.PersistentFlags().BoolVar(&noInput, "no-input", os.Getenv("RUNCONVERT_NO_INPUT") == "1", "never open the app (servers, CI); also RUNCONVERT_NO_INPUT=1")
	// `runconvert` alone in a terminal opens the app; piped, in CI or with
	// --no-input it prints help, so scripts never wait for input.
	root.Args = cobra.NoArgs
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if noInput || !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
			return cmd.Help()
		}
		return tui.Run(cmd.Context(), api.New(auth.Key(keyFlag), userAgent()), version)
	}
	root.AddCommand(convertCmd(), formatsCmd(), jobsCmd(), loginCmd(), logoutCmd(), whoamiCmd(), mcpCmd())
	root.AddCommand(helpTopics()...)

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		if e, ok := api.IsAPIError(err); ok && e.Hint() != "" {
			fmt.Fprintln(os.Stderr, e.Hint())
		}
		os.Exit(1)
	}
}
