// Package protection reads the built-in protection: on a Mac Gatekeeper,
// XProtect, System Integrity Protection and the application firewall; on
// Windows what Windows Security shows (see windows.go). It never changes any.
package protection

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/parse"
	"dev.agus.bersihin/internal/sys"
)

const day = 24 * time.Hour

var xprotectPlists = []string{
	"/Library/Apple/System/Library/CoreServices/XProtect.bundle/Contents/Info.plist",
	"/private/var/protected/xprotect/XProtect.bundle/Contents/Info.plist",
	"/System/Library/CoreServices/XProtect.bundle/Contents/Info.plist",
}

// Checker reads the protection status. Run == nil means "not on a Mac".
type Checker struct {
	Run sys.Runner
	// ModTime returns when a file was last modified; default os.Stat.
	ModTime func(path string) (time.Time, error)
	Now     func() time.Time
	// Windows, when set, makes this the Windows Checker: it runs
	// WindowsScript and returns what it printed (see windows.go).
	Windows winRun
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) modTime(p string) (time.Time, error) {
	if c.ModTime != nil {
		return c.ModTime(p)
	}
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}, err
	}
	return fi.ModTime(), nil
}

func (c *Checker) age(ms int64) string {
	days := int(c.now().Sub(time.UnixMilli(ms)) / day)
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "yesterday"
	}
	return fmt.Sprintf("%d days ago", days)
}

// freshness gives full points for a recent date, half for a middling one, none for an old or unknown one.
func (c *Checker) freshness(ms *int64, fresh, stale float64, points int) int {
	if ms == nil {
		return 0
	}
	days := float64(c.now().Sub(time.UnixMilli(*ms))) / float64(day)
	switch {
	case days <= fresh:
		return points
	case days <= stale:
		return (points + 1) / 2
	}
	return 0
}

type xprotect struct {
	version string
	updated int64
}

func (c *Checker) xprotect(ctx context.Context) *xprotect {
	var best *xprotect
	for _, plist := range xprotectPlists {
		mt, err := c.modTime(plist)
		if err != nil {
			continue
		}
		updated := mt.UnixMilli()
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, err := c.Run.Run(cctx, "/usr/bin/plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", plist)
		cancel()
		version := ""
		if err == nil {
			version = parse.PlutilRaw(res.Stdout)
		}
		if best == nil || updated > best.updated {
			best = &xprotect{version, updated}
		}
	}
	return best
}

func unknown() model.ProtectionStatus {
	return model.ProtectionStatus{Provider: "Unknown", Notes: []string{"Protection status is not available on this system"}}
}

func orUnknown(b *bool, on, off, none string, notes *[]string) {
	switch {
	case b == nil:
		*notes = append(*notes, none)
	case *b:
		*notes = append(*notes, on)
	default:
		*notes = append(*notes, off)
	}
}

func isTrue(b *bool) bool { return b != nil && *b }

// Status reads Gatekeeper, the firewall, SIP and XProtect and scores them.
func (c *Checker) Status(ctx context.Context) model.ProtectionStatus {
	if c.Windows != nil {
		return c.windowsStatus(ctx)
	}
	if c.Run == nil {
		return unknown()
	}
	var (
		wg           sync.WaitGroup
		gkOut, fwOut string
		sipOut       string
		xp           *xprotect
	)
	run := func(out *string, name string, args ...string) {
		defer wg.Done()
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		res, _ := c.Run.Run(cctx, name, args...)
		*out = res.Stdout + " " + res.Stderr
	}
	wg.Add(4)
	go run(&gkOut, "/usr/sbin/spctl", "--status")
	go run(&fwOut, "/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate")
	go run(&sipOut, "/usr/bin/csrutil", "status")
	go func() { defer wg.Done(); xp = c.xprotect(ctx) }()
	wg.Wait()

	gatekeeper := parse.Spctl(gkOut)
	firewall := parse.SocketFilterFW(fwOut)
	sip := parse.Csrutil(sipOut)

	notes := []string{}
	orUnknown(gatekeeper, "Gatekeeper is on", "Gatekeeper is off", "Could not read Gatekeeper status", &notes)
	orUnknown(firewall, "Firewall is on", "Firewall is off", "Could not read firewall status", &notes)
	if sip != nil {
		orUnknown(sip, "System Integrity Protection is on", "System Integrity Protection is off", "", &notes)
	}
	if xp != nil {
		v := ""
		if xp.version != "" {
			v = " " + xp.version
		}
		notes = append(notes, fmt.Sprintf("XProtect%s updated %s", v, c.age(xp.updated)))
	} else {
		notes = append(notes, "Could not find XProtect")
	}

	enabled := xp != nil && !(gatekeeper != nil && !*gatekeeper)
	var updated *int64
	if xp != nil {
		updated = &xp.updated
	}
	score := 0
	if isTrue(gatekeeper) {
		score += 30
	}
	if xp != nil {
		score += 20
	}
	switch {
	case sip != nil && *sip:
		score += 25
	case sip == nil:
		score += 12
	}
	if isTrue(firewall) {
		score += 15
	}
	score += c.freshness(updated, 30, 90, 10)

	st := model.ProtectionStatus{
		Provider: "XProtect + Gatekeeper", Enabled: enabled, Firewall: firewall,
		DefinitionsUpdated: updated, Score: min(max(score, 0), 100), Notes: notes,
	}
	if xp != nil {
		t := true
		st.Realtime = &t
	}
	return st
}
