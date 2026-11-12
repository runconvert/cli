package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/runconvert/cli/internal/api"
	"github.com/runconvert/cli/internal/auth"
)

func loginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login [api-key]",
		Short: "Sign in on this computer",
		Long: "Saves an API key in the system keychain (or a file only you can read).\n\n" +
			"Create a key at https://www.runconvert.com/dashboard → API keys.\n" +
			"For CI and servers, RUNCONVERT_API_KEY works without logging in.",
		Example: "  runconvert login                    # paste the key when asked\n" +
			"  runconvert login rc_live_xxxxxxxx",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := keyFlag
			if len(args) == 1 {
				key = args[0]
			}
			if key == "" {
				fmt.Fprint(os.Stderr, "Paste your API key (https://www.runconvert.com/dashboard → API keys): ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				key = strings.TrimSpace(line)
			}
			if !strings.HasPrefix(key, "rc_live_") {
				return errors.New("that isn't a RunConvert API key (they start with rc_live_)")
			}
			if _, err := api.New(key, userAgent()).ListJobs(cmd.Context(), "", 1); err != nil {
				return err
			}
			where, err := auth.SaveKey(key)
			if err != nil {
				return err
			}
			fmt.Printf("✓ Signed in. Key saved in %s.\n", where)
			return nil
		},
	}
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "logout",
		Short:   "Remove the saved API key",
		Example: "  runconvert logout",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := auth.Remove(); err != nil {
				return err
			}
			fmt.Println("✓ Signed out.")
			return nil
		},
	}
}
