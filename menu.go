package main

import (
	"github.com/egoist/mygo"
)

// menuTemplate is the app's menu bar. On a Mac it is MyGo's default menu bar
// plus "Check for Updates..." after About. On Windows, where a menu bar
// belongs to the window and the hidden title bar shows it as a popup on Alt
// or F10, it has the usual File, Edit, View and Help menus; the Mac's app
// menu (Services, Hide, Hide Others) has no counterpart there. updates is
// the updater's "Check for Updates..." item, about shows the About box.
func menuTemplate(windows bool, updates *mygo.MenuItem, about func()) []*mygo.MenuItem {
	if !windows {
		return []*mygo.MenuItem{
			{Label: "Bersihin", Submenu: []*mygo.MenuItem{
				{Role: mygo.RoleAbout},
				updates,
				mygo.Separator(),
				{Role: mygo.RoleServices},
				mygo.Separator(),
				{Role: mygo.RoleHide},
				{Role: mygo.RoleHideOthers},
				{Role: mygo.RoleUnhide},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			}},
			{Role: mygo.RoleFileMenu},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleViewMenu},
			{Role: mygo.RoleWindowMenu},
		}
	}
	return []*mygo.MenuItem{
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleQuit}, // "Exit" on Windows
		}},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleViewMenu},
		{Label: "Help", Submenu: []*mygo.MenuItem{
			updates,
			mygo.Separator(),
			{Label: "About Bersihin", Click: func(*mygo.MenuItem, *mygo.Window) { about() }},
		}},
	}
}

// showAbout shows the name and version in a message box. Click runs on the
// main thread and Dialog.Message waits for it, so the box opens from a
// goroutine.
func showAbout() {
	go mygo.Dialog.Message(mygo.MessageOptions{
		Title:   "About Bersihin",
		Message: "Bersihin " + mygo.App.Version(),
		Detail:  "Cleans up your PC safely: caches and temporary files, large and duplicate files, startup apps.",
	})
}
