# runconvert

> **Not released yet.** The first release is coming soon; until then the install commands below won't work. Updates: https://www.runconvert.com/cli

Convert files between 3,000+ formats from the terminal with
[RunConvert](https://www.runconvert.com): video, audio, images, documents,
ebooks, presentations, spreadsheets, vector, CAD, archives and fonts.

## Install

```bash
curl -fsSL https://www.runconvert.com/install.sh | sh     # macOS, Linux
irm https://www.runconvert.com/install.ps1 | iex          # Windows (PowerShell)
npm i -g @runconvert/cli                                  # or: npx @runconvert/cli
```

The scripts (in `scripts/`) download the latest release, check it against
`checksums.txt`, and install to `~/.local/bin` (Windows:
`%LOCALAPPDATA%\runconvert\bin`, added to PATH). `RUNCONVERT_VERSION` pins a
version, `RUNCONVERT_INSTALL_DIR` changes the folder.

## The app

Run `runconvert` on its own in a terminal:

1. **Drag a file or folder in**, press **Ctrl+O** to pick files in a window, or
   `/browse` inside the terminal (servers, SSH).
2. **Pick a format**: every format the file converts to, popular first, then
   grouped by category. 1–5, arrow keys, or type to filter (`ti` → TIFF).
3. **Tab** opens the options (quality, size, bitrate…) built from what the API
   accepts; they're remembered for next time.
4. When it's done: open the folder, convert again, or every file like it.

A folder shows one row per kind of file (only the biggest group starts on;
Space includes subfolders). Typing works too: `photo.heic to jpg`,
`all videos in ~/Movies to mp4`, `talk.mp4 to mp3 192k`. `/help` lists the
commands.

On servers and in CI the app never opens: piped, without a terminal, or with
`--no-input` / `RUNCONVERT_NO_INPUT=1`, `runconvert` prints help.

## Sign in

```bash
runconvert login            # opens runconvert.com: sign in, check the code, Authorize
runconvert login --no-browser   # servers and SSH: prints a link and a code
runconvert login --with-key     # or paste a key from Dashboard → API keys
runconvert whoami           # who you're signed in as, and your credits
```

Signing in makes an API key named "CLI · <this computer>", saved in the
system keychain (or a file only you can read on servers). Revoke it in
Dashboard → API keys; `runconvert logout` removes it from this computer.
In CI, set `RUNCONVERT_API_KEY` instead. `RUNCONVERT_NO_BROWSER=1` never
opens a browser.

## Convert

```bash
runconvert convert photo.heic --to jpg                       # next to the input
runconvert convert ./images/*.webp --to png --out ./png
runconvert convert "./Holiday Photos" --to jpg -r --keep-structure --out ./converted
runconvert convert ~/Downloads --only video --to mp4         # one category
runconvert convert talk.mp4 --to mp3 --opt audioBitrate=192k
runconvert convert https://example.com/report.docx --to pdf
runconvert convert *.png --to webp --json                    # for scripts
```

`runconvert convert --help` lists every flag; `runconvert help paths`,
`help options` and `help env` explain them.

## What converts to what

```bash
runconvert formats              # the 11 categories
runconvert formats video        # every video format
runconvert formats png          # everything PNG converts to, grouped
runconvert formats heic jpg     # the settings (--opt) for one conversion
runconvert jobs list            # recent conversions; jobs get / cancel <id>
```

## AI apps (MCP)

```bash
runconvert mcp setup            # how to add it to Claude Code, Claude Desktop, Cursor, VS Code
claude mcp add runconvert -- runconvert mcp
```

**Local** (`runconvert mcp`): converts files on this computer with the account
you signed in with. **Remote** (`runconvert mcp --http :8080`, deployed at
`mcp.runconvert.com`): Claude.ai and ChatGPT add
`https://mcp.runconvert.com/mcp` as a connector and sign in with OAuth
(runconvert/backend#2); it converts files from links, returns download
links, and answers within 60 s (conversions still running come back with a
job id for `get_job`, because Cloudflare ends requests at 100 s).

Tools: `list_formats` (categories, a category's formats, a format's outputs,
one conversion's options), `convert`, `get_job`, `account` (credits left).
Read-only tools say so, as the Claude and ChatGPT directories require.

The release workflow also publishes the remote server's image,
`ghcr.io/runconvert/cli/mcp:<version>` (amd64 + arm64).

## Development

```bash
go test ./...                   # every test uses local fakes, no API key needed
go build -o runconvert ./cmd/runconvert
```

Releases: tag `vX.Y.Z`. `.github/workflows/release.yml` builds macOS, Windows
and Linux (amd64, arm64) as `runconvert_<os>_<arch>.tar.gz/.zip` (no version
in the name, so `releases/latest/download/…` works), publishes npm when the
`NPM_TOKEN` secret is set, and pushes the MCP image. Homebrew, Scoop and winget
files are written but not uploaded while the repo is private.

## Layout

| Package | |
|---|---|
| `cmd/runconvert` | commands |
| `internal/api` | API client (`api.runconvert.com/v3`) |
| `internal/auth` | where the API key is kept |
| `internal/catalog` | categories, formats grouped, typed options |
| `internal/engine` | files/folders/URLs → upload → convert → download |
| `internal/intent` | "photo.heic to jpg" → a conversion |
| `internal/tui` | the app (Bubble Tea v2) |
| `internal/mcpserver` | the MCP server (local stdio and remote HTTP) |

## Roadmap

1. Phase 1: engine and commands.
2. Phase 2: the app: drag or pick files, every format, options.
3. Phase 3 (backend): device sign-in and `GET /v3/me`.
4. Phase 4: `runconvert login` through the website, `whoami`, account line in the app.
5. Phase 5: MCP for Claude, ChatGPT, Cursor and VS Code.
6. **Phase 6 (this)**: install scripts, npm, package-manager files; `/cli` page on runconvert.com.
