package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/runconvert/cli/internal/mcpserver"
)

func mcpCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "RunConvert for Claude, ChatGPT, Cursor and other AI apps (MCP)",
		Long: "Without --http: a local MCP server over stdio that converts files on this\n" +
			"computer with your signed-in account. `runconvert mcp setup` shows how to\n" +
			"add it to Claude Code, Claude Desktop, Cursor or VS Code.\n\n" +
			"With --http: the remote server (as at mcp.runconvert.com). Each request brings\n" +
			"its own key or OAuth sign-in; it converts files from links and returns links.",
		Example: "  runconvert mcp setup\n" +
			"  claude mcp add runconvert -- runconvert mcp\n" +
			"  runconvert mcp --http :8080",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if addr != "" {
				logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
				srv := &http.Server{Addr: addr, Handler: mcpserver.Handler(version, userAgent(), logger), ReadHeaderTimeout: 10 * time.Second}
				go func() {
					<-cmd.Context().Done()
					sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_ = srv.Shutdown(sctx)
				}()
				logger.Info("remote MCP server listening", "addr", addr, "version", version)
				if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
					return err
				}
				return nil
			}
			c, err := client(true)
			if err != nil {
				return err
			}
			return mcpserver.New(c, mcpserver.Local, version).Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
	cmd.Flags().StringVar(&addr, "http", "", "serve the remote MCP server over HTTP on this address, e.g. :8080")
	cmd.AddCommand(mcpSetupCmd())
	return cmd
}

// mcpSetupCmd prints how to add the local server to each app, with the full
// path to this program (desktop apps don't see the terminal's PATH).
func mcpSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup [claude-code | claude-desktop | cursor | vscode]",
		Short: "How to add RunConvert to Claude Code, Claude Desktop, Cursor or VS Code",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				exe = "runconvert"
			} else if r, err := filepath.EvalSymlinks(exe); err == nil {
				exe = r
			}
			server := map[string]any{"command": exe, "args": []string{"mcp"}}
			snippet := func(key string) string {
				b, _ := json.MarshalIndent(map[string]any{key: map[string]any{"runconvert": server}}, "", "  ")
				return string(b)
			}
			home, _ := os.UserHomeDir()
			desktop := filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
			switch runtime.GOOS {
			case "windows":
				desktop = filepath.Join(os.Getenv("APPDATA"), "Claude", "claude_desktop_config.json")
			case "linux":
				desktop = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
			}
			parts := map[string]string{
				"claude-code":    fmt.Sprintf("Claude Code: run\n\n  claude mcp add runconvert -- %q mcp\n", exe),
				"claude-desktop": fmt.Sprintf("Claude Desktop: add this to %s (Settings → Developer → Edit Config),\nthen restart Claude Desktop:\n\n%s\n", desktop, snippet("mcpServers")),
				"cursor":         fmt.Sprintf("Cursor: add this to ~/.cursor/mcp.json (or .cursor/mcp.json in a project):\n\n%s\n", snippet("mcpServers")),
				"vscode":         fmt.Sprintf("VS Code: add this to .vscode/mcp.json (or run \"MCP: Add Server\"):\n\n%s\n", snippet("servers")),
			}
			order := []string{"claude-code", "claude-desktop", "cursor", "vscode"}
			if len(args) == 1 {
				p, ok := parts[args[0]]
				if !ok {
					return fmt.Errorf("unknown app %q: claude-code, claude-desktop, cursor or vscode", args[0])
				}
				fmt.Print(p)
				return nil
			}
			fmt.Println("The local server converts files on this computer with the account you're signed in")
			fmt.Println("with (runconvert login). Pick your app:")
			fmt.Println()
			for _, k := range order {
				fmt.Println(parts[k])
			}
			fmt.Println("Claude.ai and ChatGPT: add https://mcp.runconvert.com/mcp as a connector and sign in.")
			return nil
		},
	}
}
