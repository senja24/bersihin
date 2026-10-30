package api

import (
	"context"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/snapshot"
)

// Apps lists the installed applications and moves one to the Trash.
type Apps struct{ c *core }

// List returns the apps in /Applications and ~/Applications, sorted by name.
// runID is chosen by the page: progress goes to the "progress" event with that
// id and task "apps", and it is the scan id that Apps.Uninstall needs. Pass an
// app's iconPath to System.FileIcons for its real icon.
func (a *Apps) List(ctx context.Context, runID string) ([]model.InstalledApp, error) {
	ctx, rep, end, err := a.c.begin(ctx, runID, model.TaskApps)
	if err != nil {
		return nil, err
	}
	defer end()
	list, err := a.c.apps.List(ctx, rep)
	if err != nil {
		return nil, err
	}
	if err := a.c.snaps.Put(snapshot.Apps(runID, list)); err != nil {
		return nil, err
	}
	return list, nil
}

// Uninstall moves the app with this id (from the app list scanID) to the
// Trash, with a token from Confirm.Uninstall. Apple's own apps cannot be
// removed. A refused token or an id that is not in that list is an error;
// otherwise the result says what happened.
func (a *Apps) Uninstall(ctx context.Context, scanID, id, token string) (model.UninstallResult, error) {
	if err := a.c.confirm.Redeem(token, actionUninstall, scanID, []string{id}); err != nil {
		return model.UninstallResult{}, err
	}
	snap, err := a.c.snaps.Get(scanID, model.TaskApps)
	if err != nil {
		return model.UninstallResult{}, err
	}
	if !snap.Apps[id] {
		return model.UninstallResult{}, errBadRequest
	}
	return a.c.apps.Uninstall(ctx, id), nil
}

// Startup lists the launch agents that run at login.
type Startup struct{ c *core }

// List returns the login items, the heaviest first.
func (s *Startup) List(ctx context.Context) ([]model.StartupItem, error) {
	return s.c.startup.List(ctx)
}

// Set turns a login item (an id from List) on or off, which may ask for the
// administrator password for all-users items, and returns the new list. It
// needs a token from Confirm.Startup for this id and state.
func (s *Startup) Set(ctx context.Context, id string, enabled bool, token string) ([]model.StartupItem, error) {
	if err := s.c.confirm.Redeem(token, actionStartup, "", startupSubjects(id, enabled)); err != nil {
		return nil, err
	}
	return s.c.startup.Set(ctx, id, enabled)
}

// Protection reports the Mac's built-in protection.
type Protection struct{ c *core }

// Status reads Gatekeeper, XProtect, SIP and the firewall.
func (p *Protection) Status(ctx context.Context) model.ProtectionStatus {
	return p.c.protection.Status(ctx)
}
