// Package model holds the data contract shared with the frontend. Field names
// and shapes mirror reference/cleanly/shared/types.ts (minus Tip, Feedback and
// the Windows-only values). Sizes are bytes, times are epoch milliseconds.
package model

// IconName names an icon of the frontend's sprite.
type IconName string

// The icons the frontend knows.
const (
	IconScan    IconName = "scan"
	IconBroom   IconName = "broom"
	IconShield  IconName = "shield"
	IconGauge   IconName = "gauge"
	IconApps    IconName = "apps"
	IconBubbles IconName = "bubbles"
	IconFile    IconName = "file"
	IconDupes   IconName = "dupes"
	IconHistory IconName = "history"
	IconAssist  IconName = "assist"
	IconUser    IconName = "user"
	IconGame    IconName = "game"
	IconDown    IconName = "down"
	IconChip    IconName = "chip"
	IconMore    IconName = "more"
	IconCheck   IconName = "check"
	IconArrow   IconName = "arrow"
	IconHeart   IconName = "heart"
	IconChat    IconName = "chat"
	IconGear    IconName = "gear"
	IconTrash   IconName = "trash"
	IconFolder  IconName = "folder"
	IconPhoto   IconName = "photo"
	IconVideo   IconName = "video"
	IconMusic   IconName = "music"
	IconDoc     IconName = "doc"
	IconCode    IconName = "code"
	IconCloud   IconName = "cloud"
	IconMail    IconName = "mail"
	IconGlobe   IconName = "globe"
	IconLayers  IconName = "layers"
	IconLock    IconName = "lock"
	IconPower   IconName = "power"
	IconSearch  IconName = "search"
	IconClose   IconName = "close"
	IconPlus    IconName = "plus"
	IconReveal  IconName = "reveal"
	IconSpark   IconName = "spark"
	IconWarning IconName = "warning"
	IconClock   IconName = "clock"
)

// SystemInfo describes the machine and the app.
type SystemInfo struct {
	Platform   string `json:"platform"` // always "darwin" in a real build
	Arch       string `json:"arch"`
	OSVersion  string `json:"osVersion"`
	AppVersion string `json:"appVersion"`
	HomeDir    string `json:"homeDir"`
}

// DiskInfo is one fixed local volume.
type DiskInfo struct {
	Mount string `json:"mount"` // "/" or "/Volumes/Data"
	Label string `json:"label"` // "Macintosh HD"
	Total int64  `json:"total"`
	Free  int64  `json:"free"`
	Used  int64  `json:"used"`
	// System is true for the volume the OS (and the home folder) lives on.
	System bool `json:"system"`
}

// ScanTask names the long operation a ScanProgress belongs to.
type ScanTask string

// The tasks that report progress.
const (
	TaskJunk   ScanTask = "junk"
	TaskSpaces ScanTask = "spaces"
	TaskLarge  ScanTask = "large"
	TaskDupes  ScanTask = "dupes"
	TaskClean  ScanTask = "clean"
	TaskApps   ScanTask = "apps"
	TaskTrash  ScanTask = "trash"
)

// ScanProgress is the payload of the "progress" event.
type ScanProgress struct {
	ID      string   `json:"id"` // the run id the page chose for the call
	Task    ScanTask `json:"task"`
	Phase   string   `json:"phase"` // human readable, e.g. "Browser caches"
	Done    int      `json:"done"`
	Total   int      `json:"total"` // 0 when unknown
	Current string   `json:"current,omitempty"`
}

// JunkCategory is a group of junk the app knows how to clean.
type JunkCategory struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Icon        IconName `json:"icon"`
	Size        int64    `json:"size"`
	Count       int      `json:"count"`     // top-level entries a clean would remove
	Locations   []string `json:"locations"` // display paths (with ~ for home)
	Safe        bool     `json:"safe"`      // true = selected by default
	// Irreversible is true when a clean deletes the files for good (not to the Trash).
	Irreversible bool `json:"irreversible"`
}

// CleanResult is the outcome of a clean or a move to the Trash.
type CleanResult struct {
	Freed   int64 `json:"freed"`
	Removed int   `json:"removed"`
	// Failed counts items that were in use, blocked or could not be moved. It
	// does not include Denied.
	Failed int `json:"failed"`
	// Denied counts files and folders macOS did not let us read or delete.
	Denied int `json:"denied"`
	// Skipped holds real problems only ("12 files in use").
	Skipped []string `json:"skipped"`
	// Notes are informational ("Old downloads went to the Trash"), never problems.
	Notes []string `json:"notes"`
	// Cancelled is true when the user stopped the operation before it finished.
	Cancelled bool `json:"cancelled"`
}

// SpaceKind tells folders, files and the rest apart.
type SpaceKind string

// The kinds of SpaceNode.
const (
	SpaceFolder SpaceKind = "folder"
	SpaceFile   SpaceKind = "file"
	SpaceOther  SpaceKind = "other"
)

// SpaceNode is a bubble of the space map.
type SpaceNode struct {
	Name        string      `json:"name"`
	Path        string      `json:"path"`
	Size        int64       `json:"size"`
	Kind        SpaceKind   `json:"kind"`
	Icon        IconName    `json:"icon"`
	Reclaimable int64       `json:"reclaimable"` // bytes inside that are junk or old downloads
	Children    []SpaceNode `json:"children,omitempty"`
}

// LargeFileKind classifies a large file by extension.
type LargeFileKind string

// The kinds of LargeFile.
const (
	KindVideo     LargeFileKind = "video"
	KindArchive   LargeFileKind = "archive"
	KindDiskImage LargeFileKind = "disk-image"
	KindInstaller LargeFileKind = "installer"
	KindAudio     LargeFileKind = "audio"
	KindPhoto     LargeFileKind = "photo"
	KindDocument  LargeFileKind = "document"
	KindOther     LargeFileKind = "other"
)

// LargeFile is one file found by the large file scan.
type LargeFile struct {
	Path     string        `json:"path"`
	Name     string        `json:"name"`
	Size     int64         `json:"size"`
	Modified int64         `json:"modified"`
	Accessed int64         `json:"accessed"`
	Kind     LargeFileKind `json:"kind"`
}

// LargeScan is what the large file scan returns: the files, and how many more
// big files the safety rules kept out of the list (a credentials store, a
// folder shared by a sync tool…), so that the page can say so.
type LargeScan struct {
	Files  []LargeFile `json:"files"`
	Hidden int         `json:"hidden"`
}

// DuplicateScan is what the duplicate scan returns: the groups, and how many
// copies the safety rules kept out of them (a group left with fewer than two
// copies is left out whole, its remaining copy counted too).
type DuplicateScan struct {
	Groups []DuplicateGroup `json:"groups"`
	Hidden int              `json:"hidden"`
}

// DuplicateFile is one copy inside a DuplicateGroup.
type DuplicateFile struct {
	Path     string `json:"path"`
	Modified int64  `json:"modified"`
}

// DuplicateGroup is a set of byte-identical files.
type DuplicateGroup struct {
	Hash  string          `json:"hash"`
	Size  int64           `json:"size"` // size of one copy
	Files []DuplicateFile `json:"files"`
}

// Impact is how much a startup item slows the login down.
type Impact string

// The impact levels.
const (
	ImpactHigh   Impact = "high"
	ImpactMedium Impact = "medium"
	ImpactLow    Impact = "low"
)

// StartupItem is a launch agent that runs at login.
type StartupItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	Location  string `json:"location"` // "LaunchAgents"
	Enabled   bool   `json:"enabled"`
	Impact    Impact `json:"impact"`
	CanToggle bool   `json:"canToggle"`
	// NeedsAdmin is true when toggling may ask for the administrator password.
	NeedsAdmin bool `json:"needsAdmin"`
}

// InstalledApp is an application bundle.
type InstalledApp struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Publisher string `json:"publisher,omitempty"`
	Size      int64  `json:"size,omitzero"`
	Installed int64  `json:"installed,omitzero"`
	LastUsed  int64  `json:"lastUsed,omitzero"`
	Path      string `json:"path,omitempty"`
	// IconPath is the file to take the real icon from; pass it to fileIcons.
	IconPath     string `json:"iconPath,omitempty"`
	CanUninstall bool   `json:"canUninstall"`
}

// UninstallResult is the outcome of uninstalling an app.
type UninstallResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// ProtectionStatus summarises the Mac's built-in protection.
type ProtectionStatus struct {
	Provider           string   `json:"provider"` // "XProtect + Gatekeeper"
	Enabled            bool     `json:"enabled"`
	Realtime           *bool    `json:"realtime"`
	Firewall           *bool    `json:"firewall"`
	DefinitionsUpdated *int64   `json:"definitionsUpdated"`
	LastScan           *int64   `json:"lastScan"`
	Score              int      `json:"score"` // 0..100
	Notes              []string `json:"notes"`
}

// AiProvider selects the assistant's backend.
type AiProvider string

// The assistant backends.
const (
	ProviderClaude  AiProvider = "claude"
	ProviderChatGPT AiProvider = "chatgpt"
)

// Background is the style of the app's backdrop.
type Background string

// The backdrops.
const (
	BackgroundMist  Background = "mist"
	BackgroundGrid  Background = "grid"
	BackgroundDots  Background = "dots"
	BackgroundGrain Background = "grain"
)

// Settings are the user's preferences. API keys are never part of it: only
// whether one is stored.
type Settings struct {
	AiProvider         AiProvider `json:"aiProvider"`
	HasClaudeKey       bool       `json:"hasClaudeKey"`
	HasOpenAiKey       bool       `json:"hasOpenAiKey"`
	Background         Background `json:"background"`
	LargeFileMinMb     int        `json:"largeFileMinMb"`
	ConfirmBeforeClean bool       `json:"confirmBeforeClean"`
}

// SettingsPatch is Partial<Omit<Settings, 'hasClaudeKey' | 'hasOpenAiKey'>>:
// only the fields that are set change.
type SettingsPatch struct {
	AiProvider         *AiProvider `json:"aiProvider,omitempty"`
	Background         *Background `json:"background,omitempty"`
	LargeFileMinMb     *int        `json:"largeFileMinMb,omitempty"`
	ConfirmBeforeClean *bool       `json:"confirmBeforeClean,omitempty"`
}

// AiJunk is a junk category as the assistant sees it.
type AiJunk struct {
	Name      string   `json:"name"`
	Size      int64    `json:"size"`
	Locations []string `json:"locations"`
}

// AiLargeFile is a large file as the assistant sees it.
type AiLargeFile struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

// AiSpace is a space map entry as the assistant sees it.
type AiSpace struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// AiDisk is the disk figures the assistant sees.
type AiDisk struct {
	Total int64 `json:"total"`
	Free  int64 `json:"free"`
}

// AiContext is what the assistant may see: names, paths and sizes only, never
// file contents.
type AiContext struct {
	Junk       []AiJunk      `json:"junk,omitempty"`
	LargeFiles []AiLargeFile `json:"largeFiles,omitempty"`
	Spaces     []AiSpace     `json:"spaces,omitempty"`
	Disk       *AiDisk       `json:"disk,omitempty"`
}

// Verdict is the assistant's opinion on removing a path.
type Verdict string

// The verdicts.
const (
	VerdictSafe   Verdict = "safe"
	VerdictReview Verdict = "review"
	VerdictKeep   Verdict = "keep"
)

// AiSuggestion is one verdict of an answer.
type AiSuggestion struct {
	Path    string  `json:"path"`
	Verdict Verdict `json:"verdict"`
	Reason  string  `json:"reason"`
}

// AiAdvice is the assistant's answer.
type AiAdvice struct {
	Answer      string         `json:"answer"`
	Suggestions []AiSuggestion `json:"suggestions"`
	Provider    AiProvider     `json:"provider"`
}
