package protection

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"dev.agus.bersihin/internal/model"
)

// The Windows side of the Checker. It only reads: one PowerShell run of
// WindowsScript collects what Windows Security shows, and WindowsStatus,
// which runs on any OS, scores it. Nothing is ever turned on or changed.

// WindowsScript collects the status as one line of JSON. Each part is read on
// its own, so a part that fails (a cmdlet missing on this edition, a setting
// the user may not read) is left out and reported as unknown:
//   - defender: Get-MpComputerStatus (service, antivirus, real-time
//     protection, running mode, signature and scan dates in epoch ms);
//   - av: the antivirus products Windows Security knows (SecurityCenter2),
//     to name a third-party antivirus that replaced Defender;
//   - firewall: Get-NetFirewallProfile of the active policy, per profile;
//   - smartscreen, smartscreenPolicy: the SmartScreen setting for apps and
//     files and the group policy that overrides it;
//   - bitlocker: the shell's System.Volume.BitLockerProtection of the
//     system drive, which needs no administrator rights.
const WindowsScript = `$ErrorActionPreference = 'Stop'
$o = [ordered]@{}
function ms($d) { if ($d -and $d.Year -gt 1601) { [int64]([DateTimeOffset]$d).ToUnixTimeMilliseconds() } else { $null } }
try {
  $m = Get-MpComputerStatus
  $o.defender = [ordered]@{ service = [bool]$m.AMServiceEnabled; av = [bool]$m.AntivirusEnabled; realtime = [bool]$m.RealTimeProtectionEnabled; mode = [string]$m.AMRunningMode; signatures = (ms $m.AntivirusSignatureLastUpdated); quickScan = (ms $m.QuickScanEndTime); fullScan = (ms $m.FullScanEndTime) }
} catch {}
try { $o.av = @(Get-CimInstance -Namespace root/SecurityCenter2 -ClassName AntiVirusProduct | ForEach-Object { [ordered]@{ name = [string]$_.displayName; state = [int64]$_.productState } }) } catch {}
try { $o.firewall = @(Get-NetFirewallProfile -PolicyStore ActiveStore | ForEach-Object { [ordered]@{ name = [string]$_.Name; enabled = [string]$_.Enabled } }) } catch {}
try { $o.smartscreen = [string](Get-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer' -Name SmartScreenEnabled).SmartScreenEnabled } catch {}
try { $o.smartscreenPolicy = [int](Get-ItemProperty -Path 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\System' -Name EnableSmartScreen).EnableSmartScreen } catch {}
try { $o.bitlocker = [int](New-Object -ComObject Shell.Application).NameSpace($env:SystemDrive + '\').Self.ExtendedProperty('System.Volume.BitLockerProtection') } catch {}
$o | ConvertTo-Json -Compress -Depth 4`

// EncodedCommand is s as PowerShell's -EncodedCommand wants it: base64 of
// UTF-16LE, so no quoting rule of the command line applies to the script.
func EncodedCommand(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// WinReport is the JSON WindowsScript prints.
type WinReport struct {
	Defender *struct {
		Service    bool   `json:"service"`
		AV         bool   `json:"av"`
		Realtime   bool   `json:"realtime"`
		Mode       string `json:"mode"`
		Signatures *int64 `json:"signatures"`
		QuickScan  *int64 `json:"quickScan"`
		FullScan   *int64 `json:"fullScan"`
	} `json:"defender"`
	AV []struct {
		Name  string `json:"name"`
		State int64  `json:"state"`
	} `json:"av"`
	Firewall []struct {
		Name    string `json:"name"`
		Enabled string `json:"enabled"`
	} `json:"firewall"`
	SmartScreen       *string `json:"smartscreen"`
	SmartScreenPolicy *int    `json:"smartscreenPolicy"`
	BitLocker         *int    `json:"bitlocker"`
}

// winRun runs WindowsScript; set by New on Windows.
type winRun func(ctx context.Context) (string, error)

func ptr[T any](v T) *T { return &v }

// avOn reads the productState of SecurityCenter2: its second byte is 0x10 or
// 0x11 while the product protects (0x00, 0x01 when it is off).
func avOn(state int64) bool { return (state>>8)&0x10 != 0 }

func isDefender(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "windows defender") || strings.Contains(n, "microsoft defender")
}

// WindowsStatus scores what WindowsScript reported, read at now.
func WindowsStatus(r WinReport, now time.Time) model.ProtectionStatus {
	c := &Checker{Now: func() time.Time { return now }}
	notes := []string{}
	st := model.ProtectionStatus{Provider: "Microsoft Defender", Notes: notes}

	var other string // a third-party antivirus that is on
	for _, p := range r.AV {
		if !isDefender(p.Name) && avOn(p.State) && strings.TrimSpace(p.Name) != "" {
			other = strings.TrimSpace(p.Name)
			break
		}
	}
	d := r.Defender
	defenderActive := d != nil && d.Service && d.AV && (d.Mode == "" || strings.EqualFold(d.Mode, "Normal"))
	switch {
	case other != "":
		st.Provider = other
		st.Enabled = true
		notes = append(notes, other+" protects this PC; Windows Security reports it as on")
		if d != nil && !defenderActive {
			notes = append(notes, "Microsoft Defender Antivirus is in passive mode or off, as it is beside another antivirus")
		}
	case defenderActive:
		st.Enabled = true
		st.Realtime = ptr(d.Realtime)
		if d.Realtime {
			notes = append(notes, "Microsoft Defender real-time protection is on")
		} else {
			notes = append(notes, "Microsoft Defender real-time protection is off. Turn it on in Windows Security > Virus & threat protection")
		}
		st.DefinitionsUpdated = d.Signatures
		if d.Signatures != nil {
			notes = append(notes, "Security intelligence updated "+c.age(*d.Signatures))
		} else {
			notes = append(notes, "Could not read when the security intelligence was updated")
		}
		switch {
		case d.QuickScan != nil && d.FullScan != nil:
			st.LastScan = ptr(max(*d.QuickScan, *d.FullScan))
		case d.QuickScan != nil:
			st.LastScan = d.QuickScan
		default:
			st.LastScan = d.FullScan
		}
	case d != nil:
		notes = append(notes, "Microsoft Defender Antivirus is off, and Windows Security reports no other antivirus")
	default:
		notes = append(notes, "Could not read the antivirus status")
	}

	if len(r.Firewall) > 0 {
		on, read := true, 0
		for _, p := range r.Firewall {
			switch strings.ToLower(p.Enabled) {
			case "true":
				read++
			case "false":
				read++
				on = false
				notes = append(notes, fmt.Sprintf("The firewall is off for %s networks", p.Name))
			}
		}
		if read > 0 {
			st.Firewall = ptr(on)
			if on {
				notes = append(notes, "Windows Firewall is on for every network profile")
			}
		}
	}
	if st.Firewall == nil {
		notes = append(notes, "Could not read the firewall status")
	}

	smart := smartScreen(r)
	st.Checks = append(st.Checks, model.ProtectionCheck{Label: "SmartScreen", Detail: smartDetail(r, smart), On: smart})
	bit, bitDetail := bitLocker(r.BitLocker)
	st.Checks = append(st.Checks, model.ProtectionCheck{Label: "Drive encryption", Detail: bitDetail, On: bit})

	score := 0
	if st.Enabled {
		score += 35
	}
	switch {
	case st.Realtime != nil && *st.Realtime:
		score += 25
	case st.Realtime == nil && other != "":
		score += 20 // a third-party antivirus that is on protects in real time; not read here
	}
	if isTrue(st.Firewall) {
		score += 20
	}
	if other != "" {
		score += 10 // its definitions are not reported here
	} else {
		score += c.freshness(st.DefinitionsUpdated, 7, 30, 10)
	}
	if isTrue(smart) {
		score += 10
	}
	st.Score = min(max(score, 0), 100)
	st.Notes = notes
	return st
}

// smartScreen reads SmartScreen for apps and files: the group policy wins
// over the setting; "Off" is off, any other setting on. Nil when neither is
// written, which Windows Security shows as its default (on) but is not read here.
func smartScreen(r WinReport) *bool {
	if r.SmartScreenPolicy != nil {
		return ptr(*r.SmartScreenPolicy != 0)
	}
	if r.SmartScreen == nil || *r.SmartScreen == "" {
		return nil
	}
	return ptr(!strings.EqualFold(*r.SmartScreen, "Off"))
}

func smartDetail(r WinReport, on *bool) string {
	switch {
	case on == nil:
		return "Checks apps and files from the web. Not reported by your system"
	case r.SmartScreenPolicy != nil:
		return "Checks apps and files from the web. Set by your organization's policy"
	}
	return "Checks apps and files from the web before they run"
}

// bitLocker reads System.Volume.BitLockerProtection of the system drive.
func bitLocker(v *int) (*bool, string) {
	if v == nil {
		return nil, "BitLocker on the system drive. Not reported by your system"
	}
	switch *v {
	case 1:
		return ptr(true), "BitLocker protects the system drive"
	case 3:
		return ptr(true), "BitLocker is encrypting the system drive"
	case 2:
		return ptr(false), "The system drive is not encrypted with BitLocker"
	case 4:
		return ptr(false), "BitLocker is decrypting the system drive"
	case 5:
		return ptr(false), "BitLocker protection of the system drive is suspended"
	}
	return nil, "BitLocker is not available on this PC"
}

// windowsStatus is Status on Windows.
func (c *Checker) windowsStatus(ctx context.Context) model.ProtectionStatus {
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out, err := c.Windows(cctx)
	var r WinReport
	if err != nil || json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(out, "\ufeff"))), &r) != nil {
		st := unknown()
		st.Provider = "Microsoft Defender"
		st.Notes = []string{"Could not read Windows Security. Open Windows Security to check your protection"}
		return st
	}
	return WindowsStatus(r, c.now())
}
