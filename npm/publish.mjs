// Publishes runconvert to npm from a GoReleaser dist folder:
//   one @runconvert/cli-<os>-<arch> package per binary, then the `@runconvert/cli`
//   launcher, which lists them as optionalDependencies so npm installs only
//   the one for the user's computer.
//
//   node npm/publish.mjs <version> [--dry-run]
// Needs dist/artifacts.json (written by goreleaser) and, to publish, an npm
// login or NODE_AUTH_TOKEN.
import { execFileSync } from "node:child_process"
import { chmodSync, copyFileSync, cpSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, "..")
const [version, ...flags] = process.argv.slice(2)
const dryRun = flags.includes("--dry-run")
if (!version || !/^\d+\.\d+\.\d+(-[\w.]+)?$/.test(version)) {
  console.error("usage: node npm/publish.mjs <version> [--dry-run]")
  process.exit(1)
}

const OS = { darwin: "darwin", linux: "linux", windows: "win32" }
const CPU = { amd64: "x64", arm64: "arm64" }
const meta = {
  version,
  license: "MIT",
  repository: { type: "git", url: "git+https://github.com/runconvert/runconvert-cli.git" },
  homepage: "https://www.runconvert.com/docs",
}

const artifacts = JSON.parse(readFileSync(join(root, "dist", "artifacts.json"), "utf8"))
const binaries = artifacts.filter((a) => a.type === "Binary" && OS[a.goos] && CPU[a.goarch])
if (binaries.length === 0) throw new Error("no binaries in dist/artifacts.json; run goreleaser first")

const out = join(root, "dist", "npm")
rmSync(out, { recursive: true, force: true })

const publish = (dir) => {
  const args = ["publish", "--access", "public"]
  if (dryRun) args.push("--dry-run")
  if (version.includes("-")) args.push("--tag", "next")
  execFileSync("npm", args, { cwd: dir, stdio: "inherit" })
}

const optional = {}
for (const a of binaries) {
  const os = OS[a.goos]
  const cpu = CPU[a.goarch]
  const name = `@runconvert/cli-${os}-${cpu}`
  const exe = os === "win32" ? "runconvert.exe" : "runconvert"
  const dir = join(out, `cli-${os}-${cpu}`)
  mkdirSync(join(dir, "bin"), { recursive: true })
  copyFileSync(join(root, a.path), join(dir, "bin", exe))
  chmodSync(join(dir, "bin", exe), 0o755)
  writeFileSync(
    join(dir, "package.json"),
    JSON.stringify({ name, ...meta, description: `runconvert for ${os} ${cpu}`, os: [os], cpu: [cpu], files: ["bin"], preferUnplugged: true }, null, 2),
  )
  writeFileSync(join(dir, "README.md"), `The ${os} ${cpu} build of [@runconvert/cli](https://www.npmjs.com/package/@runconvert/cli). Install \`@runconvert/cli\` instead.\n`)
  publish(dir)
  optional[name] = version
}

const main = join(out, "runconvert")
cpSync(join(here, "runconvert"), main, { recursive: true })
writeFileSync(
  join(main, "package.json"),
  JSON.stringify(
    {
      name: "@runconvert/cli",
      ...meta,
      description: "Convert files between 3,000+ formats from the terminal and AI apps (MCP) with RunConvert",
      keywords: ["file converter", "convert", "cli", "mcp", "mcp-server", "heic", "webp", "mp4", "mp3", "pdf"],
      bin: { runconvert: "bin/runconvert.js" },
      files: ["bin", "README.md"],
      engines: { node: ">=18" },
      optionalDependencies: optional,
    },
    null,
    2,
  ),
)
publish(main)
console.log(`${dryRun ? "Dry run: would publish" : "Published"} @runconvert/cli ${version} and ${binaries.length} platform packages`)
