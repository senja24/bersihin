// Package parse turns the output of macOS command line tools into values. It
// does no I/O, so every parser is tested against captured sample output in
// testdata/macos.
package parse

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Plist is a property list converted by `plutil -convert json -o -`.
type Plist map[string]any

// PlutilJSON decodes the JSON that plutil printed.
func PlutilJSON(out []byte) (Plist, error) {
	var p Plist
	if err := json.Unmarshal(bytes.TrimSpace(out), &p); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errors.New("plist is not a dictionary")
	}
	return p, nil
}

// String returns the non-empty string at key.
func (p Plist) String(key string) string {
	s, _ := p[key].(string)
	return s
}

// Strings returns the array at key as strings; non-string items are skipped.
func (p Plist) Strings(key string) []string {
	arr, _ := p[key].([]any)
	var out []string
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Bool returns the boolean at key and whether it was present as a boolean.
func (p Plist) Bool(key string) (v, ok bool) {
	v, ok = p[key].(bool)
	return v, ok
}

// Bundle is what an app's Info.plist says about it.
type Bundle struct {
	Name    string // CFBundleDisplayName, "" if absent
	ID      string // CFBundleIdentifier
	Version string // CFBundleShortVersionString, else CFBundleVersion
}

// BundleInfo reads the fields of an app's Info.plist.
func BundleInfo(p Plist) Bundle {
	v := p.String("CFBundleShortVersionString")
	if v == "" {
		v = p.String("CFBundleVersion")
	}
	return Bundle{Name: p.String("CFBundleDisplayName"), ID: p.String("CFBundleIdentifier"), Version: v}
}

// LaunchAgent is the part of a launchd plist the app shows.
type LaunchAgent struct {
	Label    string
	Program  string
	Args     []string
	Disabled bool // Disabled key set to true
}

// LaunchAgentInfo reads a launchd job plist. fallbackLabel is used when the
// plist has no Label.
func LaunchAgentInfo(p Plist, fallbackLabel string) LaunchAgent {
	a := LaunchAgent{Label: p.String("Label"), Program: p.String("Program"), Args: p.Strings("ProgramArguments")}
	if a.Label == "" {
		a.Label = fallbackLabel
	}
	if a.Program == "" && len(a.Args) > 0 {
		a.Program = a.Args[0]
	}
	a.Disabled, _ = p.Bool("Disabled")
	return a
}

// Command is the command line a launch agent runs.
func (a LaunchAgent) Command() string {
	if len(a.Args) > 0 {
		return strings.Join(a.Args, " ")
	}
	return a.Program
}

// LaunchctlList returns the labels in the output of `launchctl list`
// (tab separated PID, status, label, after a header line).
func LaunchctlList(out string) map[string]bool {
	labels := map[string]bool{}
	lines := strings.Split(out, "\n")
	if len(lines) > 0 {
		lines = lines[1:]
	}
	for _, line := range lines {
		f := strings.Split(line, "\t")
		if len(f) > 2 {
			if l := strings.TrimSpace(f[2]); l != "" {
				labels[l] = true
			}
		}
	}
	return labels
}

var disabledRe = regexp.MustCompile(`"([^"]+)"\s*=>\s*(\w+)`)

// LaunchctlPrintDisabled returns, per label, whether launchd's persistent
// override disables it, from `launchctl print-disabled gui/<uid>`.
func LaunchctlPrintDisabled(out string) map[string]bool {
	m := map[string]bool{}
	for _, g := range disabledRe.FindAllStringSubmatch(out, -1) {
		m[g[1]] = g[2] == "disabled" || g[2] == "true"
	}
	return m
}

// MountEntry is one line of `mount`.
type MountEntry struct {
	Device string
	Mount  string
	// Options are the lower-cased parenthesised words: the file system type
	// first, then flags such as "local" or "read-only".
	Options []string
}

// FSType is the file system type.
func (e MountEntry) FSType() string {
	if len(e.Options) == 0 {
		return ""
	}
	return e.Options[0]
}

// Has reports whether the option is set.
func (e MountEntry) Has(opt string) bool {
	for _, o := range e.Options {
		if o == opt {
			return true
		}
	}
	return false
}

var mountRe = regexp.MustCompile(`^(.+?) on (.+) \(([^)]*)\)$`)

// Mount parses the output of `/sbin/mount`.
func Mount(out string) []MountEntry {
	var entries []MountEntry
	for line := range strings.SplitSeq(out, "\n") {
		g := mountRe.FindStringSubmatch(strings.TrimSpace(line))
		if g == nil {
			continue
		}
		var opts []string
		for o := range strings.SplitSeq(g[3], ",") {
			opts = append(opts, strings.ToLower(strings.TrimSpace(o)))
		}
		entries = append(entries, MountEntry{Device: g[1], Mount: g[2], Options: opts})
	}
	return entries
}

var hdiutilRe = regexp.MustCompile(`\t(/Volumes/[^\t]+)\s*$`)

// HdiutilMounts returns the mount points of mounted disk images, from
// `hdiutil info`.
func HdiutilMounts(out string) map[string]bool {
	m := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		if g := hdiutilRe.FindStringSubmatch(line); g != nil {
			m[strings.TrimSpace(g[1])] = true
		}
	}
	return m
}

var duRe = regexp.MustCompile(`^(\d+)\t(.+)$`)

// DU parses `du -sk path…` into bytes per path.
func DU(out string) map[string]int64 {
	m := map[string]int64{}
	for line := range strings.SplitSeq(out, "\n") {
		if g := duRe.FindStringSubmatch(line); g != nil {
			if kb, err := strconv.ParseInt(g[1], 10, 64); err == nil {
				m[g[2]] = kb * 1024
			}
		}
	}
	return m
}

// mdls prints "2026-10-19 16:41:26 +0000" or, with a 12-hour clock,
// "2026-10-19 4:41:26<U+202F>PM +0000": the hour may be unpadded, and AM/PM
// follows a plain space, a narrow no-break space (U+202F) or a no-break space.
var mdlsRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2}) (\d{1,2}):(\d{2}):(\d{2})(?:[ \x{202F}\x{00A0}]?([AaPp][Mm]))? ([+-])(\d{2})(\d{2})$`)

// MdlsDates parses `mdls -raw -name <attr> file…` for n files: with several
// files the values are separated by NUL. The result has one epoch-ms entry
// per file, 0 where mdls had no date ("(null)") or the value is not a date.
// It returns nil when the output does not hold n values.
func MdlsDates(out string, n int) []int64 {
	dates, _ := MdlsDatesChecked(out, n)
	return dates
}

// MdlsDatesChecked is MdlsDates that also counts the values that are neither
// "(null)" nor a date it can read (another locale's AM/PM words or layout), so
// the caller can tell "never used" from "could not read".
func MdlsDatesChecked(out string, n int) (dates []int64, unparsed int) {
	vals := strings.Split(out, "\x00")
	if len(vals) < n {
		return nil, 0
	}
	dates = make([]int64, n)
	for i := range n {
		v := strings.TrimSpace(vals[i])
		dates[i] = mdlsDate(v)
		if dates[i] == 0 && v != "(null)" && v != "" {
			unparsed++
		}
	}
	return dates, unparsed
}

func mdlsDate(v string) int64 {
	g := mdlsRe.FindStringSubmatch(v)
	if g == nil {
		return 0
	}
	num := func(s string) int { n, _ := strconv.Atoi(s); return n }
	year, month, day := num(g[1]), num(g[2]), num(g[3])
	hour, min, sec := num(g[4]), num(g[5]), num(g[6])
	if g[7] != "" {
		if hour < 1 || hour > 12 {
			return 0
		}
		hour %= 12
		if strings.EqualFold(g[7], "PM") {
			hour += 12
		}
	}
	zh, zm := num(g[9]), num(g[10])
	if hour > 23 || min > 59 || sec > 59 || zh > 23 || zm > 59 {
		return 0
	}
	off := zh*3600 + zm*60
	if g[8] == "-" {
		off = -off
	}
	t := time.Date(year, time.Month(month), day, hour, min, sec, 0, time.FixedZone("", off))
	if t.Day() != day || int(t.Month()) != month { // 31 February and the like
		return 0
	}
	if ms := t.UnixMilli(); ms > 0 {
		return ms
	}
	return 0
}

// Spctl reads `spctl --status`: Gatekeeper on, off, or unknown (nil).
func Spctl(out string) *bool {
	return match(out, `(?i)assessments enabled`, `(?i)assessments disabled`)
}

// Csrutil reads `csrutil status`: System Integrity Protection on, off or unknown.
func Csrutil(out string) *bool {
	return match(out, `(?i)status:\s*enabled`, `(?i)status:\s*disabled`)
}

// SocketFilterFW reads `socketfilterfw --getglobalstate`: firewall on, off or unknown.
func SocketFilterFW(out string) *bool {
	return match(out, `(?i)enabled|State = [12]`, `(?i)disabled|State = 0`)
}

func match(out, on, off string) *bool {
	var v bool
	switch {
	case regexp.MustCompile(on).MatchString(out):
		v = true
	case regexp.MustCompile(off).MatchString(out):
		v = false
	default:
		return nil
	}
	return &v
}

// PlutilRaw reads `plutil -extract key raw -o -`: the bare value.
func PlutilRaw(out string) string { return strings.TrimSpace(out) }
