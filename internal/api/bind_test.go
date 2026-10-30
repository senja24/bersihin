package api

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo"
)

var bindOnce sync.Once

type noShell struct{}

func (noShell) TrashItem(string) error               { return nil }
func (noShell) ShowItemInFolder(string)              {}
func (noShell) OpenExternal(string) error            { return nil }
func (noShell) FileIcon(string, int) ([]byte, error) { return nil, nil }

// TestBindAndGenerate binds every service with MyGo (which panics on a method
// whose types JSON cannot carry) and writes the TypeScript client to a temp
// file, to check the names the frontend will see.
func TestBindAndGenerate(t *testing.T) {
	// MyGo's registry is global: bind once, however often the test runs
	bindOnce.Do(func() { mygo.Bind(New(Deps{Home: t.TempDir(), Shell: noShell{}}).All()...) })
	out := filepath.Join(t.TempDir(), "mygo.ts")
	if err := mygo.WriteTypeScript(out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	ts := string(b)
	for _, want := range wantTS {
		if !strings.Contains(ts, want) {
			t.Errorf("generated client lacks %q", want)
		}
	}
}

var wantTS = []string{
	"export const System = {", "info(", "disk(", "disks(", "fileIcons(paths: string[])", "revealPath(path: string)",
	"openExternal(link: string)", "cancelScan(runID: string)",
	"export const Junk = {", "clean(runID: string, scanID: string, ids: string[], token: string)", "scan(runID: string)",
	"export const Files = {", "scanSpaces(runID: string, root: string)", "scanLargeFiles(runID: string, minBytes: number)", "scanDuplicates(runID: string)", "trashPaths(runID: string, scanID: string, paths: string[], token: string)",
	"export const Apps = {", "list(runID: string)", "uninstall(scanID: string, id: string, token: string)",
	"export const Startup = {", "set(id: string, enabled: boolean, token: string)",
	"export const Confirm = {", "clean(scanID: string, ids: string[])", "trash(scanID: string, paths: string[])", "uninstall(scanID: string, id: string)", "startup(id: string, enabled: boolean)",
	"export const Protection = {", "status(): Promise<ProtectionStatus>",
	"export const Settings = {", "get(): Promise<Settings>", "set(patch: SettingsPatch)", "setApiKey(provider: AiProvider, key: string | null, forBaseUrl: string)",
	"export const Assistant = {", "ask(question: string, data: AiContext): Promise<AiAdvice>", "testConnection(provider: AiProvider, draft: AiEndpointPatch | null)",
	"export const events", "progress",
}
