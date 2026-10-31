# Changelog

## 0.2.0 (unreleased)

- Windows support (preview), for Windows 10 and 11 on x64 and Arm64: Smart Scan, Cleanup, Large Files, Duplicates, Space Map and the Assistant.
- Cleanup on Windows: the Temp folder, crash dumps, Chrome, Edge, Brave, Vivaldi, Opera and Firefox caches, npm, pnpm, Yarn, pip and Gradle caches, and old downloads. Only folders of your own profile; nothing that needs administrator rights.
- Your files go to the Recycle Bin on Windows, through the undoable shell operation, and are left alone when Windows would delete them for good instead.
- Windows safety rules: system folders, AppData (with its Microsoft credential stores), `.ssh` and other credential folders, OneDrive and your profile folders are never offered; paths compare without case; network shares, alternate data streams and short 8.3 names are refused; junctions are never followed.
- Applications on Windows: the programs of the registry's Uninstall keys, with size and install date. Uninstall opens the program's own uninstaller after you confirm; Windows' own parts and runtimes are locked; Bersihin never elevates.
- Speed on Windows: Run keys, Startup folders and sign-in tasks. Your own startup apps are turned on and off as Task Manager does (StartupApproved); those of every user and tasks are read-only.
- Protection on Windows, read-only: Microsoft Defender or the antivirus that replaced it, real-time protection, security intelligence age, last scan, firewall profiles, SmartScreen and BitLocker.
- Cleanup on Windows can empty the Recycle Bin (never ticked by default), and only if nothing was deleted since the scan.
- Old downloads on Windows also count a file opened through File Explorer (Recent Items) as used.
- Windows menu bar: File, Edit, View and Help, in place of the Mac's app menu.
- The page says Recycle Bin, File Explorer and PC on Windows, and shows Windows paths.
- Fix: on Windows a refused connection made the assistant's connection test report "unexpected answer" instead of "refused".
- CI on Windows: `go vet`, the build, the whole Go suite as a gate (tests of the Mac's rules skip themselves, with the reason) and a Windows app build on every push.

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
