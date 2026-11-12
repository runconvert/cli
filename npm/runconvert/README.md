# runconvert

Convert files between 3,000+ formats from the terminal, and from Claude,
ChatGPT, Cursor and VS Code (MCP), with [RunConvert](https://www.runconvert.com/cli).

```bash
npx runconvert login          # sign in through runconvert.com
npx runconvert                # the app: drag files in, pick a format
npx runconvert convert photo.heic --to jpg
```

MCP (Claude Desktop, Cursor, VS Code):

```json
{ "mcpServers": { "runconvert": { "command": "npx", "args": ["-y", "runconvert", "mcp"] } } }
```

Docs: https://www.runconvert.com/docs#cli
