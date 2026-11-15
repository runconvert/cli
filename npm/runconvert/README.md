# @runconvert/cli

Convert files between 3,000+ formats from the terminal, and from Claude,
ChatGPT, Cursor and VS Code (MCP), with [RunConvert](https://www.runconvert.com/cli).

```bash
npm i -g @runconvert/cli         # then run: runconvert

# or without installing:
npx @runconvert/cli login          # sign in through runconvert.com
npx @runconvert/cli                # the app: drag files in, pick a format
npx @runconvert/cli convert photo.heic --to jpg
```

MCP (Claude Desktop, Cursor, VS Code):

```json
{ "mcpServers": { "runconvert": { "command": "npx", "args": ["-y", "@runconvert/cli", "mcp"] } } }
```

Docs: https://www.runconvert.com/docs#cli
