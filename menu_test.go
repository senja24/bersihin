package main

import (
	"testing"

	"github.com/egoist/mygo"
)

func labels(items []*mygo.MenuItem) []string {
	var out []string
	for _, it := range items {
		if it.Label != "" {
			out = append(out, it.Label)
		} else {
			out = append(out, string(it.Role))
		}
	}
	return out
}

func TestMacMenuUnchanged(t *testing.T) {
	updates := &mygo.MenuItem{Label: "Check for Updates..."}
	m := menuTemplate(false, updates, func() {})
	if got := labels(m); len(got) != 5 || got[0] != "Bersihin" || got[4] != string(mygo.RoleWindowMenu) {
		t.Fatalf("menus = %v", got)
	}
	app := m[0].Submenu
	if app[0].Role != mygo.RoleAbout || app[1] != updates || app[3].Role != mygo.RoleServices || app[len(app)-1].Role != mygo.RoleQuit {
		t.Fatalf("app menu = %v", labels(app))
	}
}

func TestWindowsMenu(t *testing.T) {
	updates := &mygo.MenuItem{Label: "Check for Updates..."}
	about := 0
	m := menuTemplate(true, updates, func() { about++ })
	got := labels(m)
	want := []string{"File", string(mygo.RoleEditMenu), string(mygo.RoleViewMenu), "Help"}
	if len(got) != len(want) {
		t.Fatalf("menus = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("menus = %v", got)
		}
	}
	// no macOS-only roles anywhere
	var walk func([]*mygo.MenuItem)
	walk = func(items []*mygo.MenuItem) {
		for _, it := range items {
			switch it.Role {
			case mygo.RoleAbout, mygo.RoleServices, mygo.RoleHide, mygo.RoleHideOthers, mygo.RoleUnhide, mygo.RoleAppMenu, mygo.RoleWindowMenu:
				t.Errorf("macOS role %q in the Windows menu", it.Role)
			}
			walk(it.Submenu)
		}
	}
	walk(m)
	if m[0].Submenu[0].Role != mygo.RoleQuit {
		t.Fatal("File has no Exit")
	}
	help := m[3].Submenu
	if help[0] != updates || help[2].Click == nil {
		t.Fatalf("help = %v", labels(help))
	}
	help[2].Click(help[2], nil)
	if about != 1 {
		t.Fatal("About did not run")
	}
}
