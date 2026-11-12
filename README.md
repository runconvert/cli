# runconvert

Convert files between 3,000+ formats from the terminal with
[RunConvert](https://www.runconvert.com): video, audio, images, documents,
ebooks, presentations, spreadsheets, vector, CAD, archives and fonts.

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

## Development

```bash
go test ./...                   # every test uses local fakes, no API key needed
go build -o runconvert ./cmd/runconvert
```

Releases: tag `vX.Y.Z`; `.github/workflows/release.yml` builds macOS, Windows
and Linux (amd64, arm64) with GoReleaser.

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

## Roadmap

1. Phase 1: engine and commands.
2. Phase 2: the app: drag or pick files, every format, options.
3. Phase 3 (backend): device sign-in and `GET /v3/me`.
4. **Phase 4 (this)**: `runconvert login` through the website, `whoami`, account line in the app.
5. MCP for Claude, ChatGPT and Cursor.
6. `/cli` page on runconvert.com, npm, install script, first release.
