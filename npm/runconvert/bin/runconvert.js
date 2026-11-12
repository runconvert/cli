#!/usr/bin/env node
// Starts the runconvert program for this computer, installed by npm from the
// matching @runconvert/cli-<os>-<arch> package.
"use strict"
const { spawnSync } = require("node:child_process")

const pkg = `@runconvert/cli-${process.platform}-${process.arch}`
const exe = process.platform === "win32" ? "runconvert.exe" : "runconvert"

let bin
try {
  bin = require.resolve(`${pkg}/bin/${exe}`)
} catch {
  console.error(
    `runconvert: no build for ${process.platform}/${process.arch} was installed (${pkg}).\n` +
      "Reinstall without --no-optional / --omit=optional, or download it from https://www.runconvert.com/docs.",
  )
  process.exit(1)
}

const r = spawnSync(bin, process.argv.slice(2), { stdio: "inherit", windowsHide: true })
if (r.error) {
  console.error(`runconvert: ${r.error.message}`)
  process.exit(1)
}
if (r.signal) process.kill(process.pid, r.signal)
process.exit(r.status ?? 1)
