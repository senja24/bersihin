// Package api holds the services bound to the page with mygo.Bind. Every
// exported method of a service type is callable from the frontend (the
// generated client lowercases the first letter: System.Disks -> System.disks),
// so helpers here are unexported.
//
// Long operations take a context.Context. MyGo cancels it when the page
// navigates away or the window closes; a Cancel button calls
// System.CancelScan with the run id the page chose for the call.
//
// Calls that delete or change something (Junk.Clean, Files.TrashPaths,
// Apps.Uninstall, Startup.Set) refuse to run without a confirmation token from
// the Confirm service, bound to the exact ids or paths and to the scan they
// came from. The page asks for a token only after the user confirmed.
package api

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"dev.agus.bersihin/internal/apps"
	"dev.agus.bersihin/internal/assistant"
	"dev.agus.bersihin/internal/confirm"
	"dev.agus.bersihin/internal/disks"
	"dev.agus.bersihin/internal/dupes"
	"dev.agus.bersihin/internal/icons"
	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/protection"
	"dev.agus.bersihin/internal/recheck"
	"dev.agus.bersihin/internal/settings"
	"dev.agus.bersihin/internal/snapshot"
	"dev.agus.bersihin/internal/startup"
	"dev.agus.bersihin/internal/sys"
	"dev.agus.bersihin/internal/trash"
)

// Progress is sent to every window while a scan or a clean runs, at most
// about every 120 ms, plus a final update with the phase "Done". The page
// listens with events.progress.on(...) of the generated client.
var Progress = mygo.NewEvent[model.ScanProgress]("progress")

// WindowFocus tells a page whether its window has the keyboard focus. The page
// cannot learn this alone: WKWebView reports no blur for a window that was
// never focused, so the animations would run for a window behind other apps.
var WindowFocus = mygo.NewEvent[bool]("window:focus")

// Shell is the part of mygo.Shell the services use.
type Shell interface {
	TrashItem(path string) error
	ShowItemInFolder(path string)
	OpenExternal(url string) error
	FileIcon(path string, size int) ([]byte, error)
}

// Deps is everything the services need from outside.
type Deps struct {
	Home  string
	Shell Shell
	// Sink receives progress; default: broadcast the Progress event.
	Sink progress.Sink
	// AppVersion returns the app's version, evaluated when asked.
	AppVersion func() string
	// Run runs macOS tools; default: real processes.
	Run sys.Runner
	// Disks lists volumes; default: disks.New(Home).
	Disks *disks.Lister
	// DataDir returns the folder for settings.json (MyGo's user data folder).
	DataDir func() (string, error)
	// Secrets is the Keychain, for the API keys.
	Secrets settings.Secrets
	// HTTP is the client for the assistant; default: a plain client (the
	// request itself carries the 60 second limit).
	HTTP *http.Client
	// AnthropicURL and OpenAIURL override the assistant's endpoints (tests).
	AnthropicURL, OpenAIURL string
	// Now is the clock for confirmation tokens, scan snapshots and the cutoffs
	// of a junk scan; default: time.Now.
	Now func() time.Time
}

// Services are the bound services. A nil service is not bound.
type Services struct {
	System     *System
	Junk       *Junk
	Files      *Files
	Apps       *Apps
	Startup    *Startup
	Confirm    *Confirm
	Protection *Protection
	Settings   *Settings
	Assistant  *Assistant
}

// All returns the services for mygo.Bind.
func (s Services) All() []any {
	var out []any
	for _, svc := range []any{s.System, s.Junk, s.Files, s.Apps, s.Startup, s.Confirm, s.Protection, s.Settings, s.Assistant} {
		if !isNil(svc) {
			out = append(out, svc)
		}
	}
	return out
}

// New wires the services.
func New(d Deps) Services {
	c := newCore(d)
	return Services{
		System: &System{c: c},
		Junk:   &Junk{c: c},
		Files:  &Files{c: c},

		Apps:       &Apps{c: c},
		Startup:    &Startup{c: c},
		Confirm:    &Confirm{c: c},
		Protection: &Protection{c: c},
		Settings:   &Settings{c: c},
		Assistant:  &Assistant{c: c},
	}
}

func isNil(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// core is the state the services share.
type core struct {
	deps    Deps
	scans   *progress.Scans
	confirm *confirm.Store
	snaps   *snapshot.Store
	// hash reads a whole file for the duplicate re-check; tests replace it.
	hash recheck.HashFunc
	// moves is held while files are checked and moved to the Trash.
	moves   chan struct{}
	disks   *disks.Lister
	trasher *trash.Trasher
	icons   *icons.Provider

	apps       *apps.Manager
	startup    *startup.Manager
	protection *protection.Checker
	settings   *settings.Store
	assistant  *assistant.Service
}

func newCore(d Deps) *core {
	if d.Sink == nil {
		d.Sink = func(p model.ScanProgress) { _ = Progress.Broadcast(p) }
	}
	if d.Run == nil {
		d.Run = sys.Exec{}
	}
	if d.AppVersion == nil {
		d.AppVersion = func() string { return "" }
	}
	if d.Disks == nil {
		d.Disks = disks.New(d.Home)
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	c := &core{
		deps: d, scans: &progress.Scans{}, disks: d.Disks, hash: dupes.FullHashRegular, moves: make(chan struct{}, 1),
		confirm: &confirm.Store{Now: d.Now}, snaps: &snapshot.Store{Now: d.Now},
	}
	c.trasher = &trash.Trasher{
		Guard: func(ctx context.Context) pathsafe.Guard {
			return pathsafe.ForPlatform(d.Home, c.disks.DataMounts(ctx))
		},
		Move: d.Shell.TrashItem,
	}
	c.icons = &icons.Provider{
		Home:        d.Home,
		Icon:        d.Shell.FileIcon,
		LocalMounts: func(ctx context.Context) []string { return c.disks.DataMounts(ctx) },
	}
	c.apps = apps.New(d.Home, d.Shell.TrashItem)
	c.startup = startup.New(d.Home)
	c.protection = protection.New()
	if d.DataDir == nil {
		d.DataDir = func() (string, error) { return "", errors.New("no data folder") }
	}
	c.settings = &settings.Store{Dir: d.DataDir, Secrets: d.Secrets}
	c.assistant = &assistant.Service{
		Home:          d.Home,
		Provider:      c.settings.Provider,
		Key:           c.settings.Key,
		KeyFor:        c.settings.KeyFor,
		OfficialModel: c.settings.OfficialModel,
		Endpoint:      c.settings.Endpoint,
		Version:       d.AppVersion,
		HTTP:          d.HTTP,
		AnthropicURL:  d.AnthropicURL,
		OpenAIURL:     d.OpenAIURL,
	}
	return c
}

// begin registers a cancellable run under the page's run id and returns its
// context, reporter and end func. The id must be valid and unused: not running
// and not the id of a remembered scan.
func (c *core) begin(ctx context.Context, runID string, task model.ScanTask) (context.Context, progress.Reporter, func(), error) {
	// Reserve first, then look at the stored scans: a scan stores its results
	// before it releases its id, so an id that was free a moment ago cannot
	// slip between the two checks.
	ctx, end, err := c.scans.Begin(ctx, runID, task)
	if err != nil {
		return ctx, nil, nil, err
	}
	// an id also stays taken while its scan is remembered or a token for it is live
	if c.snaps.Has(runID) || c.confirm.Pending(runID) {
		end()
		return ctx, nil, nil, progress.ErrDuplicateID
	}
	return ctx, progress.Throttle(c.deps.Sink, runID, task), end, nil
}

var errBadRequest = errors.New("Invalid request")

func (c *core) systemInfo(ctx context.Context) model.SystemInfo {
	info := model.SystemInfo{
		Platform: runtime.GOOS, Arch: runtime.GOARCH, AppVersion: c.deps.AppVersion(), HomeDir: c.deps.Home,
	}
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if r, err := c.deps.Run.Run(ctx, "/usr/bin/sw_vers", "-productVersion"); err == nil {
			info.OSVersion = strings.TrimSpace(r.Stdout)
		}
	}
	return info
}

// lockMoves waits for its turn to move files to the Trash, or for ctx to end.
func (c *core) lockMoves(ctx context.Context) (func(), error) {
	release := func() { <-c.moves }
	select {
	case c.moves <- struct{}{}: // free: take it, even if ctx has ended (the run then reports cancelled)
		return release, nil
	default:
	}
	select {
	case c.moves <- struct{}{}:
		return release, nil
	case <-ctx.Done():
		return nil, progress.ErrCancelled
	}
}
