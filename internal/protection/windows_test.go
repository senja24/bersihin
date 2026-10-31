package protection

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"dev.agus.bersihin/internal/model"
)

var winNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func daysAgo(d int) int64 { return winNow.Add(-time.Duration(d) * 24 * time.Hour).UnixMilli() }

func WindowsStatusFromJSON(t *testing.T, out string) model.ProtectionStatus {
	t.Helper()
	c := &Checker{Now: func() time.Time { return winNow }, Windows: func(context.Context) (string, error) { return out, nil }}
	return c.Status(context.Background())
}

func TestWindowsDefenderHealthy(t *testing.T) {
	out := `{"defender":{"service":true,"av":true,"realtime":true,"mode":"Normal","signatures":` + itoa(daysAgo(1)) + `,"quickScan":` + itoa(daysAgo(3)) + `,"fullScan":` + itoa(daysAgo(20)) + `},` +
		`"av":[{"name":"Windows Defender","state":397568}],"firewall":[{"name":"Domain","enabled":"True"},{"name":"Private","enabled":"True"},{"name":"Public","enabled":"True"}],` +
		`"smartscreen":"Warn","bitlocker":1}`
	c := &Checker{Now: func() time.Time { return winNow }, Windows: func(context.Context) (string, error) { return "\ufeff" + out + "\r\n", nil }}
	st := c.Status(context.Background())
	if st.Provider != "Microsoft Defender" || !st.Enabled || !isTrue(st.Realtime) || !isTrue(st.Firewall) || st.Score != 100 {
		t.Fatalf("%+v", st)
	}
	if st.DefinitionsUpdated == nil || *st.DefinitionsUpdated != daysAgo(1) || st.LastScan == nil || *st.LastScan != daysAgo(3) {
		t.Fatalf("dates: %+v", st)
	}
	if len(st.Checks) != 2 || st.Checks[0].Label != "SmartScreen" || !isTrue(st.Checks[0].On) || !isTrue(st.Checks[1].On) {
		t.Fatalf("checks: %+v", st.Checks)
	}
	if !strings.Contains(strings.Join(st.Notes, "\n"), "updated yesterday") {
		t.Fatalf("notes: %v", st.Notes)
	}
}

func TestWindowsProblemsReported(t *testing.T) {
	out := `{"defender":{"service":true,"av":true,"realtime":false,"mode":"Normal","signatures":` + itoa(daysAgo(40)) + `},` +
		`"firewall":[{"name":"Domain","enabled":"True"},{"name":"Public","enabled":"False"}],"smartscreen":"Off","bitlocker":2}`
	c := &Checker{Now: func() time.Time { return winNow }, Windows: func(context.Context) (string, error) { return out, nil }}
	st := c.Status(context.Background())
	if !st.Enabled || st.Realtime == nil || *st.Realtime || st.Firewall == nil || *st.Firewall || st.LastScan != nil {
		t.Fatalf("%+v", st)
	}
	if st.Score != 35 {
		t.Fatalf("score = %d", st.Score)
	}
	notes := strings.Join(st.Notes, "\n")
	for _, want := range []string{"real-time protection is off", "off for Public networks", "40 days ago"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q: %v", want, st.Notes)
		}
	}
	if st.Checks[0].On == nil || *st.Checks[0].On || st.Checks[1].On == nil || *st.Checks[1].On {
		t.Fatalf("checks: %+v", st.Checks)
	}
}

func TestWindowsThirdPartyAntivirus(t *testing.T) {
	out := `{"defender":{"service":true,"av":false,"realtime":false,"mode":"Passive Mode"},` +
		`"av":[{"name":"Windows Defender","state":393472},{"name":"Norton 360","state":266240}],"smartscreenPolicy":1}`
	c := &Checker{Now: func() time.Time { return winNow }, Windows: func(context.Context) (string, error) { return out, nil }}
	st := c.Status(context.Background())
	if st.Provider != "Norton 360" || !st.Enabled || st.Realtime != nil || st.Firewall != nil {
		t.Fatalf("%+v", st)
	}
	if !isTrue(st.Checks[0].On) || !strings.Contains(st.Checks[0].Detail, "policy") || st.Checks[1].On != nil {
		t.Fatalf("checks: %+v", st.Checks)
	}
	if !strings.Contains(strings.Join(st.Notes, "\n"), "passive mode") {
		t.Fatalf("notes: %v", st.Notes)
	}
}

func TestWindowsNothingRead(t *testing.T) {
	for _, out := range []string{"{}", "not json"} {
		c := &Checker{Now: func() time.Time { return winNow }, Windows: func(context.Context) (string, error) { return out, nil }}
		st := c.Status(context.Background())
		if st.Enabled || st.Realtime != nil || st.Firewall != nil || st.Score != 0 {
			t.Fatalf("%q: %+v", out, st)
		}
	}
	c := &Checker{Windows: func(context.Context) (string, error) { return "", errors.New("no powershell") }}
	if st := c.Status(context.Background()); st.Enabled || !strings.Contains(st.Notes[0], "Windows Security") {
		t.Fatalf("%+v", st)
	}
}

func TestWindowsDefenderOff(t *testing.T) {
	out := `{"defender":{"service":true,"av":false,"realtime":false,"mode":"Normal"},"av":[{"name":"Windows Defender","state":393472}]}`
	st := WindowsStatusFromJSON(t, out)
	if st.Enabled || !strings.Contains(st.Notes[0], "is off") {
		t.Fatalf("%+v", st)
	}
}

func TestWindowsScriptReadsOnly(t *testing.T) {
	low := strings.ToLower(WindowsScript)
	for _, verb := range []string{"set-", "enable-", "disable-", "remove-", "new-item", "start-", "invoke-", "add-", "update-mp"} {
		if strings.Contains(low, verb) {
			t.Errorf("the script uses %q", verb)
		}
	}
	b, err := base64.StdEncoding.DecodeString(EncodedCommand("a€"))
	if err != nil || len(b) != 4 || string(utf16.Decode([]uint16{uint16(b[0]) | uint16(b[1])<<8, uint16(b[2]) | uint16(b[3])<<8})) != "a€" {
		t.Fatalf("EncodedCommand: % x %v", b, err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
