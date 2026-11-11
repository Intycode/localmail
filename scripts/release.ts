#!/usr/bin/env bun
/**
 * release — version bump + signed builds + GitHub Release with updater manifest.
 *
 *   bun run release bump 0.2.0   bump versions, commit, tag v0.2.0, push
 *   bun run release build        build THIS platform (macOS Apple Silicon / Windows x64),
 *                                upload to the draft release, merge latest.json;
 *                                once every PLATFORMS entry is in, publishes it as latest
 *
 * See CLAUDE.md → "Rilasci" for the full procedure and prerequisites.
 */
import { $ } from "bun"
import { existsSync, readFileSync, statSync, writeFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"

const ROOT = join(import.meta.dir, "..")
const REPO = "Intycode/localmail"
// a release stays draft until latest.json covers all of these
const PLATFORMS = ["darwin-aarch64", "windows-x86_64"]
$.cwd(ROOT)

const readJson = (p: string) => JSON.parse(readFileSync(join(ROOT, p), "utf8"))
const version = (): string => readJson("package.json").version
const die = (msg: string): never => {
  console.error(`release: ${msg}`)
  process.exit(1)
}
const t0 = Date.now()
const step = (msg: string) => {
  const s = Math.round((Date.now() - t0) / 1000)
  console.log(`\n\x1b[1;36m▶ ${msg}\x1b[0m  \x1b[2m(${Math.floor(s / 60)}m${String(s % 60).padStart(2, "0")}s)\x1b[0m`)
}
const mb = (f: string) => `${(statSync(f).size / 1e6).toFixed(1)} MB`

async function bump(next: string | undefined): Promise<void> {
  if (!next || !/^\d+\.\d+\.\d+$/.test(next)) die("usage: release bump <x.y.z>")
  const cur = version().split(".").map(Number)
  const nxt = next!.split(".").map(Number)
  const newer = nxt[0] - cur[0] || nxt[1] - cur[1] || nxt[2] - cur[2]
  if (newer <= 0) die(`${next} is not greater than ${version()} (the updater only installs newer versions)`)
  if ((await $`git status --porcelain`.text()).trim()) die("working tree not clean: commit first")

  for (const p of ["package.json", "apps/client/package.json"]) {
    const s = readFileSync(join(ROOT, p), "utf8")
    writeFileSync(join(ROOT, p), s.replace(/"version": "[^"]+"/, `"version": "${next}"`))
  }
  const cargo = join(ROOT, "src-tauri/Cargo.toml")
  writeFileSync(cargo, readFileSync(cargo, "utf8").replace(/^version = "[^"]+"/m, `version = "${next}"`))
  await $`cargo update --workspace --offline`.cwd(join(ROOT, "src-tauri")) // syncs Cargo.lock

  await $`git add package.json apps/client/package.json src-tauri/Cargo.toml src-tauri/Cargo.lock`
  await $`git commit -m ${`chore(release): v${next}`}`
  await $`git tag -a v${next} -m v${next}` // annotated: --follow-tags pushes only these
  await $`git push --follow-tags`
  console.log(`v${next} tagged and pushed. Next: bun run release build (on macOS and on Windows)`)
}

function signingEnv(): Record<string, string> {
  if (readFileSync(join(ROOT, "src-tauri/tauri.conf.json"), "utf8").includes("REPLACE_WITH_UPDATER_PUBKEY"))
    die("updater pubkey not set in src-tauri/tauri.conf.json (see CLAUDE.md)")
  const env = { ...process.env } as Record<string, string>
  if (!env.TAURI_SIGNING_PRIVATE_KEY) {
    const key = join(homedir(), ".tauri", "localmail.key")
    if (!existsSync(key)) die(`TAURI_SIGNING_PRIVATE_KEY not set and ${key} missing`)
    env.TAURI_SIGNING_PRIVATE_KEY = readFileSync(key, "utf8")
  }
  if (env.TAURI_SIGNING_PRIVATE_KEY_PASSWORD === undefined) die("TAURI_SIGNING_PRIVATE_KEY_PASSWORD not set")
  return env
}

type Built = { files: string[]; updater: string; platforms: string[] }

async function buildMac(env: Record<string, string>): Promise<Built> {
  if (!env.APPLE_SIGNING_IDENTITY)
    console.warn("release: APPLE_SIGNING_IDENTITY not set → ad-hoc signature only, not notarized (users must \"Open Anyway\")")
  // Apple Silicon only (shortcut: no Intel Macs; for them build --target universal-apple-darwin with a lipo'd sidecar)
  if (process.arch !== "arm64") die("macOS release must be built on Apple Silicon")
  step("Motore Go (aarch64)")
  await $`bun run engine:build`.env(env)
  // frontend is built by tauri.conf.json → beforeBuildCommand
  step("Frontend + app Tauri + .app/.dmg + firma updater (diversi minuti)")
  await $`bunx tauri build --config src-tauri/tauri.conf.json --bundles app,dmg`.env(env)
  const b = join(ROOT, "src-tauri/target/release/bundle")
  const updater = join(b, "macos/LocalMail.app.tar.gz")
  return { files: [updater, `${updater}.sig`, join(b, `dmg/LocalMail_${version()}_aarch64.dmg`)], updater, platforms: ["darwin-aarch64"] }
}

async function buildWindows(env: Record<string, string>): Promise<Built> {
  step("Motore Go (x64)")
  await $`bun run engine:build`.env(env)
  step("Frontend + app Tauri + installer NSIS + firma updater (diversi minuti)")
  await $`bunx tauri build --config src-tauri/tauri.conf.json --bundles nsis`.env(env)
  step("Firma Authenticode (token USB)")
  await $`bash scripts/sign-windows.sh`.env(env)
  const updater = join(ROOT, `src-tauri/target/release/bundle/nsis/LocalMail_${version()}_x64-setup.exe`)
  return { files: [updater, `${updater}.sig`], updater, platforms: ["windows-x86_64"] }
}

async function build(): Promise<void> {
  const v = version()
  if (!(await $`git tag --points-at HEAD`.text()).split("\n").includes(`v${v}`))
    die(`HEAD is not tagged v${v}: run "release bump" first (or git pull --tags)`)
  // output is piped through Bun, not a TTY: force cargo's colors and progress bar
  const env = { CARGO_TERM_COLOR: "always", CARGO_TERM_PROGRESS_WHEN: "always", CARGO_TERM_PROGRESS_WIDTH: "80", ...signingEnv() }
  step(`Build v${v} per ${process.platform}/${process.arch}`)
  const built =
    process.platform === "darwin" ? await buildMac(env)
    : process.platform === "win32" ? await buildWindows(env)
    : die(`unsupported platform ${process.platform}`)
  for (const f of built.files) if (!existsSync(f)) die(`missing artifact ${f}`)
  step("Artefatti")
  for (const f of built.files) console.log(`  ${f.slice(ROOT.length + 1)}  ${mb(f)}`)

  const tag = `v${v}`
  step(`Release ${tag} su GitHub`)
  if ((await $`gh release view ${tag} -R ${REPO}`.nothrow().quiet()).exitCode !== 0)
    await $`gh release create ${tag} -R ${REPO} --draft --verify-tag --title ${tag} --generate-notes`
  step(`Upload ${built.files.length} file (${built.files.map(mb).join(" + ")})`)
  await $`gh release upload ${tag} -R ${REPO} --clobber ${built.files}`

  step("Aggiorno latest.json")
  // merge this platform into latest.json (the other OS may have uploaded already)
  const dl = await $`gh release download ${tag} -R ${REPO} -p latest.json -O -`.nothrow().quiet()
  const manifest = dl.exitCode === 0 ? JSON.parse(dl.stdout.toString()) : { platforms: {} }
  manifest.version = v
  manifest.pub_date = new Date().toISOString()
  // GitHub turns spaces into dots in asset names; ours have none
  const name = built.updater.split(/[\\/]/).pop()!
  for (const p of built.platforms)
    manifest.platforms[p] = {
      signature: readFileSync(`${built.updater}.sig`, "utf8").trim(),
      url: `https://github.com/${REPO}/releases/download/${tag}/${name}`,
    }
  const out = join(ROOT, "src-tauri/target/latest.json")
  writeFileSync(out, JSON.stringify(manifest, null, 2))
  await $`gh release upload ${tag} -R ${REPO} --clobber ${out}`

  const missing = PLATFORMS.filter((p) => !manifest.platforms[p])
  if (missing.length) {
    step("Fatto: release in draft")
    console.log(`${tag}: manca ${missing.join(", ")} → builda sull'altro OS, poi verrà pubblicata`)
    return
  }
  step(`Pubblico ${tag}`)
  await $`gh release edit ${tag} -R ${REPO} --draft=false --latest`
  step("Fatto")
  console.log(`${tag} pubblicata: le app installate propongono l'aggiornamento al prossimo avvio`)
}

const [cmd, arg] = process.argv.slice(2)
if (cmd === "bump") await bump(arg)
else if (cmd === "build") await build()
else die("usage: release bump <x.y.z> | build")
