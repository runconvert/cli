package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func jobsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "List, inspect and cancel conversions",
		Example: "  runconvert jobs list --status finished --limit 5\n" +
			"  runconvert jobs get <job-id>\n" +
			"  runconvert jobs cancel <job-id>",
	}
	var status string
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "Recent conversions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client(true)
			if err != nil {
				return err
			}
			jobs, err := c.ListJobs(cmd.Context(), status, limit)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			defer w.Flush()
			fmt.Fprintln(w, "ID\tSTATUS\tCREDITS\tCREATED")
			for _, j := range jobs {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", j.ID, j.Status, j.CreditsUsed, j.CreatedAt.Local().Format("2006-01-02 15:04"))
			}
			return nil
		},
	}
	list.Flags().StringVar(&status, "status", "", "waiting, processing, finished or error")
	list.Flags().IntVar(&limit, "limit", 20, "how many")
	get := &cobra.Command{
		Use:   "get <job-id>",
		Short: "A conversion's details and download links, as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client(true)
			if err != nil {
				return err
			}
			j, err := c.GetJob(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(j)
		},
	}
	cancel := &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "Stop a running conversion",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client(true)
			if err != nil {
				return err
			}
			if err := c.CancelJob(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Println("✓ Cancelled", args[0])
			return nil
		},
	}
	cmd.AddCommand(list, get, cancel)
	return cmd
}
