package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/runconvert/cli/internal/catalog"
)

func formatsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "formats [category | from] [to]",
		Aliases: []string{"format", "fmt"},
		Short:   "What converts to what: categories, formats and options",
		Example: "  runconvert formats              # the 11 categories\n" +
			"  runconvert formats video        # every video format\n" +
			"  runconvert formats png          # everything PNG converts to, grouped\n" +
			"  runconvert formats heic jpg     # the options for one conversion",
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, _ := client(false)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			defer w.Flush()
			out := func(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }

			if len(args) == 0 {
				cats, all, err := c.Catalog(ctx, "")
				if err != nil {
					return err
				}
				if asJSON {
					return out(map[string]any{"categories": cats, "formats": all})
				}
				count := map[string]int{}
				for _, f := range all {
					count[f.Category]++
				}
				fmt.Fprintf(w, "%d input formats in %d categories:\n\nCATEGORY\tFORMATS\n", len(all), len(cats))
				for _, cat := range cats {
					fmt.Fprintf(w, "%s\t%d\n", cat, count[cat])
				}
				fmt.Fprintln(w, "\nList one: runconvert formats video · what one converts to: runconvert formats png")
				return nil
			}

			first := strings.TrimPrefix(strings.ToLower(args[0]), ".")
			if cat := catalog.Categories[first]; cat != "" && len(args) == 1 {
				_, list, err := c.Catalog(ctx, cat)
				if err != nil {
					return err
				}
				if asJSON {
					return out(list)
				}
				fmt.Fprintf(w, "%d %s formats:\n\nFORMAT\tCONVERTS TO\n", len(list), cat)
				for _, f := range list {
					n := 0
					for _, g := range catalog.Grouped(&f) {
						n += len(g.Formats)
					}
					fmt.Fprintf(w, "%s\t%d formats\n", f.Format, n)
				}
				return nil
			}

			if len(args) == 1 {
				f, err := c.Outputs(ctx, first)
				if err != nil {
					return err
				}
				groups := catalog.Grouped(f)
				if asJSON {
					return out(groups)
				}
				n := 0
				for _, g := range groups {
					n += len(g.Formats)
				}
				fmt.Fprintf(w, "%s (%s) converts to %d formats:\n\n", strings.ToUpper(first), f.Category, n)
				for _, g := range groups {
					var names []string
					for _, o := range g.Formats {
						name := o.Format
						if o.Credits > 1 {
							name += fmt.Sprintf(" (%d cr)", o.Credits)
						}
						names = append(names, name)
					}
					fmt.Fprintf(w, "%s\t%s\n", g.Name, strings.Join(names, "  "))
				}
				fmt.Fprintf(w, "\nOptions for one: runconvert formats %s jpg\n", first)
				return nil
			}

			to := strings.TrimPrefix(strings.ToLower(args[1]), ".")
			engines, err := c.Engines(ctx, first, to)
			if err != nil {
				return err
			}
			if asJSON {
				return out(engines)
			}
			for _, e := range engines {
				fmt.Fprintf(w, "%s → %s with %s, %d credit(s)\n\nOPTION (--opt name=value)\tTYPE\tVALUES\tDESCRIPTION\n", first, to, e.Name, e.Credits)
				for _, o := range e.Options {
					vals, _ := json.Marshal(o.SupportedValues)
					v := string(vals)
					if len(v) > 40 {
						v = v[:37] + "..."
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", o.Name, o.Type, v, o.Description)
				}
				fmt.Fprintln(w)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}
