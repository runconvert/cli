# runconvert

Convert files between 3,000+ formats from the terminal with
[RunConvert](https://www.runconvert.com): video, audio, images, documents,
ebooks, presentations, spreadsheets, vector, CAD, archives and fonts.

## Sign in

```bash
runconvert login            # paste a key from runconvert.com/dashboard → API keys
```

The key is saved in the system keychain (or a file only you can read on
servers). In CI, set `RUNCONVERT_API_KEY` instead.

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

## Roadmap

1. **Phase 1 (this)**: engine and commands.
2. Interactive app (`runconvert` on its own): drag or pick files, every format, options.
3–4. Sign in through the website (`runconvert login` opens the browser).
5. MCP for Claude, ChatGPT and Cursor.
6. `/cli` page on runconvert.com, npm, install script, first release.
