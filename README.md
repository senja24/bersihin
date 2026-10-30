# Bersihin

A macOS disk cleaner. Smart Scan, cache cleanup, large files, duplicates, startup items, app uninstall and a space map, in one small native app built with Go and [MyGo](https://mygo.egoist.dev/) (React UI in the system WebKit view, no Chromium).

macOS only. Apple silicon and Intel.

## What it scans

Everything is read-only until you confirm an action.

| Screen | What it looks at |
|---|---|
| **Cleanup** | User caches (`~/Library/Caches`, minus browser, developer and system-owned caches), logs (`~/Library/Logs`), browser caches (Chrome, Brave, Edge, Arc, Safari, Vivaldi, Opera, Firefox, every profile), Xcode leftovers (`DerivedData`, `iOS DeviceSupport`, `CoreSimulator/Caches`), developer caches (npm, Yarn, pnpm, pip, Gradle), the Trash (`~/.Trash`), and old downloads (top-level items in `~/Downloads` untouched for 90+ days). User caches, logs and browser caches are ticked by default; the rest you tick yourself. |
| **Large Files** | Files of at least 500 MB (change it in Settings, 10 MB to 100 GB) in your home folder and on other local data drives, biggest 300 shown. Skips `~/Library`, hidden folders, app and library packages, git repositories, VM and container stores, and other volumes. |
| **Duplicates** | Files of 1 MB or more in Desktop, Documents, Downloads, Pictures, Movies and Music, compared by size, then a quick hash, then a full SHA-256. Skips iCloud files that are not downloaded, hard links and packages. The newest copy stays. |
| **Space Map** | How your home folder (two levels deep) and each local drive is used, with what can be freed in each folder. |
| **Speed** | Startup items in `~/Library/LaunchAgents` and `/Library/LaunchAgents`. |
| **Applications** | Apps in `/Applications` and `~/Applications`, with size and last-used date. Apple's own apps are locked. |
| **Protection** | Gatekeeper, the application firewall, System Integrity Protection and the XProtect version. Read-only. |
| **Smart Scan** | Runs Cleanup's scan and maps your home folder, then sums it up. |
| **Assistant** | Optional. With your own Claude or OpenAI key, it sends file names, paths (home shown as `~`) and sizes, never file contents, and shows you exactly what will be sent first. |

## Install

The app is not signed with a Developer ID or notarized yet, so macOS blocks it on first launch. After installing, clear the quarantine flag once:

1. Open `bersihin-<version>.dmg` and drag **Bersihin** to **Applications**.
2. Run:

   ```sh
   xattr -cr /Applications/Bersihin.app
   ```

3. Open Bersihin from Applications.

macOS asks for access to Desktop, Documents and Downloads the first time a scan reads them. Turning a system-wide startup item on or off asks for your admin password.

## Develop

Requirements: macOS, [Go](https://go.dev/dl/) 1.27.3 or later, [Bun](https://bun.sh).

```sh
bun install
bun run dev        # runs the app with hot reload (mygo dev)
bun run dev:web    # UI only, in the browser, with demo data
```

Tests and checks:

```sh
go test ./...
bun test
bun run typecheck
```

## Build

```sh
bun run build                                      # this Mac's architecture
bun run build -- -platform darwin/universal        # Apple silicon + Intel
```

The app and the disk image land in `build/` (for example `build/darwin-universal/Bersihin.app` and `bersihin-0.1.0.dmg`). Builds are signed ad hoc; see Install for the `xattr` step on other Macs.

## How it treats your files

- Caches and logs are deleted permanently, and only inside their own folders. Files changed in the last 24 hours are kept.
- Everything else (large files, duplicates, old downloads, apps) goes to the Trash, so you can put it back.
- Protected locations are never offered: system folders, `~/Library` (outside the cache cleanup), credentials such as `~/.ssh`, cloud-synced folders, app and library packages, and other volumes.
- Every destructive action asks first, and the app re-checks each file right before it moves it.
