package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type flagGroup struct {
	title string
	names []string
}

// groupFlags replaces a command's flat flag list with titled groups.
func groupFlags(cmd *cobra.Command, groups []flagGroup) {
	cmd.SetUsageFunc(func(c *cobra.Command) error {
		w := c.OutOrStderr()
		fmt.Fprintf(w, "Usage:\n  %s\n", c.UseLine())
		if c.Example != "" {
			fmt.Fprintf(w, "\nExamples:\n%s\n", c.Example)
		}
		listed := map[string]bool{"help": true}
		for _, g := range groups {
			fs := pflag.NewFlagSet(g.title, pflag.ContinueOnError)
			for _, n := range g.names {
				if f := c.Flags().Lookup(n); f != nil {
					fs.AddFlag(f)
					listed[n] = true
				}
			}
			fmt.Fprintf(w, "\n%s:\n%s", g.title, fs.FlagUsages())
		}
		rest := pflag.NewFlagSet("rest", pflag.ContinueOnError)
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if !listed[f.Name] {
				rest.AddFlag(f)
			}
		})
		if rest.HasFlags() {
			fmt.Fprintf(w, "\nOther:\n%s", rest.FlagUsages())
		}
		fmt.Fprintf(w, "\nGlobal:\n%s  -h, --help         show this help\n", c.InheritedFlags().FlagUsages())
		fmt.Fprintf(w, "\nMore: runconvert help paths | help options | help env\n")
		return nil
	})
	cmd.SetHelpFunc(func(c *cobra.Command, _ []string) {
		fmt.Fprintf(c.OutOrStdout(), "%s\n\n", strings.TrimSpace(c.Long))
		_ = c.UsageFunc()(c)
	})
}

// helpTopics are `runconvert help <topic>` pages with no command of their own.
func helpTopics() []*cobra.Command {
	topic := func(name, short, long string) *cobra.Command {
		return &cobra.Command{Use: name, Short: short, Long: strings.TrimSpace(long)}
	}
	return []*cobra.Command{
		topic("paths", "Inputs and output paths: files, folders, globs, URLs, --out, -o patterns", `
INPUTS (any mix, as many as you like)
  photo.heic                  a file
  "./Holiday Photos"          a folder: its files (add -r for subfolders; hidden files are skipped)
  ./images/*.webp             a glob pattern (quote it on Windows: "./images/*.webp")
  https://example.com/a.docx  a public URL, fetched by RunConvert

  The input format comes from the file extension. If there is none, add --from:
    runconvert convert ./download --from pdf --to docx

OUTPUTS
  default      next to each input, same name, new extension:  photo.heic → photo.jpg
               (URLs are saved in the current folder)
  --out DIR    all results in DIR:        --out ./converted
  -o PATTERN   build each path yourself:  -o "./audio/{name}.{ext}"

  Pattern parts:
    {dir}   the input's folder (with --keep-structure, its subfolder too)
    {name}  the input's name without extension
    {ext}   the new format, e.g. jpg
    {from}  the old format, e.g. heic

  --keep-structure   with folder inputs, recreate subfolders under --out
  --overwrite        replace results that already exist (default: skip them)

EXAMPLES
  runconvert convert "./Holiday Photos" --to jpg -r --keep-structure --out ./converted
  runconvert convert *.mov --to mp4 -o "{dir}/mp4/{name}.{ext}"
  runconvert convert report.docx --to pdf -o "./{name}-{from}.{ext}"   # report-docx.pdf`),

		topic("options", "Conversion settings with --opt: quality, bitrate, size, trimming…", `
Each conversion has its own settings. List them, with their types and allowed
values, for any pair:

  runconvert formats heic jpg
  runconvert formats mp4 mp3

Then pass them with --opt name=value (repeat --opt for more). Values are typed
from what the API declares: numbers, true/false, or text.

  runconvert convert talk.mp4 --to mp3 --opt audioBitrate=192k
  runconvert convert clip.mp4 --to mp3 --opt trimStart=00:00:10.000 --opt trimEnd=00:01:00.000
  runconvert convert photo.heic --to jpg --opt strip_metadata=true

An unknown name is rejected before anything is uploaded. Each conversion costs
credits; formats shows how many.`),

		topic("env", "Environment variables and where the API key is stored", `
RUNCONVERT_API_KEY   API key, used when --key isn't given (CI, scripts, MCP configs)
RUNCONVERT_API_URL   API address (default https://api.runconvert.com)

The key is looked for in this order: --key, RUNCONVERT_API_KEY, then the key
saved by runconvert login. login saves it in the system keychain (macOS
Keychain, Windows Credential Manager, Linux Secret Service). Where there is
none (servers, containers), it goes in a file only your user can read:
  macOS    ~/Library/Application Support/runconvert/config.json
  Windows  %AppData%\runconvert\config.json
  Linux    ~/.config/runconvert/config.json

runconvert logout deletes it.`),
	}
}
