package winreg

import (
	"slices"
	"testing"
)

func TestFake(t *testing.T) {
	f := &Fake{Denied: []string{`HKLM\Software\Locked`}}
	f.Set(LocalMachine, `Software\A\B`, "Name", Value{Type: TypeString, Str: "x\x00"})
	f.Set(LocalMachine, `Software\A\C`, "Size", Value{Type: TypeDWord, Num: 7})
	f.Key(CurrentUser, `Software\A\D`)

	subs, err := f.SubKeys(LocalMachine, `software\a`)
	if err != nil || !slices.Equal(subs, []string{"B", "C"}) {
		t.Fatalf("SubKeys = %v, %v", subs, err)
	}
	if _, err := f.SubKeys(LocalMachine, `Software\Nope`); err != ErrNotExist {
		t.Fatalf("missing key: %v", err)
	}
	v, _ := f.Values(LocalMachine, `Software\A\B`)
	if Str(v, "name") != "x" {
		t.Fatalf("Str = %q", Str(v, "name"))
	}
	v, _ = f.Values(LocalMachine, `Software\A\C`)
	if n, ok := Num(v, "Size"); !ok || n != 7 || Str(v, "Size") != "" {
		t.Fatalf("Num = %d %v", n, ok)
	}
	if err := f.SetBinary(LocalMachine, `Software\Locked`, "x", []byte{1}); err == nil {
		t.Fatal("a denied key was written")
	}
	if err := f.SetBinary(CurrentUser, `Software\A\D`, "x", []byte{2}); err != nil || f.Writes != 1 {
		t.Fatalf("SetBinary: %v, writes %d", err, f.Writes)
	}
}
