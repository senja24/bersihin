package main

import (
	"errors"
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"

	"dev.agus.bersihin/internal/api"
	"dev.agus.bersihin/internal/navguard"
	"dev.agus.bersihin/internal/settings"
)

// keychain is the login Keychain, through mygo.Secrets.
type keychain struct{}

func (keychain) Get(key string) ([]byte, error) {
	v, err := mygo.Secrets.Get(key)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return nil, settings.ErrNotFound
	}
	return v, err
}
func (keychain) Set(key string, v []byte) error { return mygo.Secrets.Set(key, v) }
func (keychain) Delete(key string) error        { return mygo.Secrets.Delete(key) }

// shell hands the services the parts of mygo.Shell they use.
type shell struct{}

func (shell) TrashItem(path string) error              { return mygo.Shell.TrashItem(path) }
func (shell) ShowItemInFolder(path string)             { mygo.Shell.ShowItemInFolder(path) }
func (shell) OpenExternal(url string) error            { return mygo.Shell.OpenExternal(url) }
func (shell) FileIcon(p string, n int) ([]byte, error) { return mygo.Shell.FileIcon(p, n) }

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	// The services the page can call (see internal/api); progress reaches it
	// through the "progress" event.
	mygo.Bind(api.New(api.Deps{
		Home:       home,
		Shell:      shell{},
		AppVersion: mygo.App.Version,
		DataDir:    func() (string, error) { return mygo.App.Path(mygo.PathUserData) },
		Secrets:    keychain{},
	}).All()...)

	// One running copy: a second launch hands over to the first and exits.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	// Sparkle-style update window: checks 10 s after launch, then daily.
	mygo.Use(updater.Plugin)
	// MyGo's default menu bar plus "Check for Updates..." after About.
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Bersihin", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			updater.MenuItem(),
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
	}))
	// Light appearance only, whatever the system uses.
	mygo.Theme.SetSource(mygo.ThemeLight)

	var mainWin windowRef
	mygo.App.OnSecondInstance(func(args []string, workingDir string) { mainWin.raise() })

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:                "Bersihin",
			Width:                1320,
			Height:               840,
			MinWidth:             1120,
			MinHeight:            740,
			BackgroundColor:      "#f2f3f6", // --color-paper in src/styles/tokens.css
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 20, Y: 16},
			// Windows: the window buttons fill the page's title bar, --titlebar-height
			TitleBarHeight: 44,
			// The frontend: devUrl during `mygo dev`, frontendDist
			// embedded by `mygo build` otherwise (see mygo.config.ts).
			URL: "/",
		})
		// The window stays on the app's own pages: MyGo would trust file:,
		// about: and custom schemes with the bridge to Go.
		devOrigin := ""
		if mygo.IsDev() {
			devOrigin = navguard.DevOrigin(os.Getenv("MYGO_DEV_URL"))
		}
		win.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
			switch navguard.Decide(e.URL, devOrigin, e.UserInitiated) {
			case navguard.Allow:
			case navguard.OpenExternally:
				e.PreventDefault()
				go mygo.Shell.OpenExternal(e.URL)
			default:
				e.PreventDefault()
			}
		})
		// window.open() and target=_blank never open a window; MyGo's default
		// would send any http(s) URL to the browser, script-started or not.
		win.Page().SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
			if navguard.OpenFromNewWindow(req.URL, req.UserInitiated) {
				go mygo.Shell.OpenExternal(req.URL)
			}
			return nil
		})
		// The page pauses its animations while the window is not focused.
		win.OnFocus(func() { api.WindowFocus.Emit(win, true) })
		win.OnBlur(func() { api.WindowFocus.Emit(win, false) })
		api.WindowFocus.Emit(win, win.IsFocused())
		mainWin.set(win)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
