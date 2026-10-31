# Changelog

## 0.2.0 (unreleased)

- Windows support (preview), for Windows 10 and 11 on x64 and Arm64: Smart Scan, Cleanup, Large Files, Duplicates, Space Map and the Assistant.
- Cleanup on Windows: the Temp folder, crash dumps, Chrome, Edge, Brave, Vivaldi, Opera and Firefox caches, npm, pnpm, Yarn, pip and Gradle caches, and old downloads. Only folders of your own profile; nothing that needs administrator rights.
- Your files go to the Recycle Bin on Windows, through the undoable shell operation, and are left alone when Windows would delete them for good instead.
- Windows safety rules: system folders, AppData (with its Microsoft credential stores), `.ssh` and other credential folders, OneDrive and your profile folders are never offered; paths compare without case; network shares, alternate data streams and short 8.3 names are refused; junctions are never followed.
- The page says Recycle Bin and File Explorer on Windows, and shows Windows paths. Applications, Speed and Protection stay macOS only for now and are hidden on Windows.
- CI on Windows: `go vet`, the build, the Windows tests and a Windows app build on every push.

## 0.1.3

- Choose the model for Claude and ChatGPT in Settings (leave it empty for the default).
- API keys for Claude-compatible and OpenAI-compatible services are now tied to the exact address they were saved for, and Settings shows when a key belongs to a different address. Keys saved with an earlier version need to be entered once more.
- Simpler background picker in Settings.

## 0.1.2

- Bersihin now updates itself: it checks for new versions in the background, shows what changed, and installs the update when you choose, then restarts.
- Added "Check for Updates..." to the Bersihin menu.
- New app icon.

## 0.1.1

- Lower idle CPU and memory use: animations pause when the window is not active and the background mist is drawn on the GPU (about 0.4% CPU in the background).

## 0.1.0

- First release.
- Smart Scan, Cleanup, Large Files, Duplicates, Space Map, Speed, Applications and Protection.
- Assistant with Claude and OpenAI.
