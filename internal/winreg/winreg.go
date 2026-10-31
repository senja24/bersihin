// Package winreg reads and writes the few registry keys Bersihin uses on
// Windows. The callers take a Registry, so their rules run and are tested on
// any OS against a Fake; System, in winreg_windows.go, is the real registry.
package winreg

import (
	"errors"
	"slices"
	"strings"
	"sync"
)

// Root is a registry hive.
type Root int

// The hives Bersihin reads.
const (
	LocalMachine Root = iota // HKEY_LOCAL_MACHINE
	CurrentUser              // HKEY_CURRENT_USER
)

func (r Root) String() string {
	if r == LocalMachine {
		return "HKLM"
	}
	return "HKCU"
}

// The value types Bersihin understands.
const (
	TypeString       = 1 // REG_SZ
	TypeExpandString = 2 // REG_EXPAND_SZ
	TypeBinary       = 3 // REG_BINARY
	TypeDWord        = 4 // REG_DWORD
	TypeQWord        = 11
)

// Value is one registry value. Str holds REG_SZ and REG_EXPAND_SZ (not
// expanded), Num holds REG_DWORD and REG_QWORD, Bin holds REG_BINARY.
type Value struct {
	Type uint32
	Str  string
	Num  uint64
	Bin  []byte
}

// ErrNotExist is returned for a key that does not exist.
var ErrNotExist = errors.New("registry key does not exist")

// Registry is what the Windows services need of the registry. Paths are
// below the root, with backslashes: `Software\Microsoft\...`. Writes only ever
// go to binary values.
type Registry interface {
	// SubKeys lists the names of the keys directly below path.
	SubKeys(root Root, path string) ([]string, error)
	// Values returns the values of the key at path, by name.
	Values(root Root, path string) (map[string]Value, error)
	// SetBinary writes a REG_BINARY value, creating the key if need be.
	SetBinary(root Root, path, name string, data []byte) error
}

// Str returns the string value name of vals, "" when there is none or it is not a string.
func Str(vals map[string]Value, name string) string {
	v, ok := lookup(vals, name)
	if !ok || (v.Type != TypeString && v.Type != TypeExpandString) {
		return ""
	}
	return strings.TrimRight(v.Str, "\x00")
}

// Num returns the DWORD or QWORD value name of vals, and whether there is one.
func Num(vals map[string]Value, name string) (uint64, bool) {
	v, ok := lookup(vals, name)
	if !ok || (v.Type != TypeDWord && v.Type != TypeQWord) {
		return 0, false
	}
	return v.Num, true
}

// lookup finds a value by name, ignoring case as the registry does.
func lookup(vals map[string]Value, name string) (Value, bool) {
	if v, ok := vals[name]; ok {
		return v, true
	}
	for k, v := range vals {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return Value{}, false
}

// Fake is a registry in memory, for tests. Key paths ignore case.
type Fake struct {
	mu   sync.Mutex
	keys map[string]map[string]Value // "HKLM\path" (lower case) -> values
	name map[string]string           // lower-case key -> its spelling
	// Denied lists keys (as "HKLM\path") whose writes fail, as an
	// HKEY_LOCAL_MACHINE key does for a user who is not elevated.
	Denied []string
	// Writes counts SetBinary calls that succeeded.
	Writes int
}

func fakeKey(root Root, path string) string {
	return strings.ToLower(root.String() + `\` + strings.Trim(path, `\`))
}

// Set adds a value, creating its key and the keys above it.
func (f *Fake) Set(root Root, path, name string, v Value) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure(root, path)
	if name != "" || v.Type != 0 {
		f.keys[fakeKey(root, path)][name] = v
	}
}

// Key creates an empty key.
func (f *Fake) Key(root Root, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure(root, path)
}

func (f *Fake) ensure(root Root, path string) {
	if f.keys == nil {
		f.keys, f.name = map[string]map[string]Value{}, map[string]string{}
	}
	parts := strings.Split(strings.Trim(path, `\`), `\`)
	for i := range parts {
		p := strings.Join(parts[:i+1], `\`)
		k := fakeKey(root, p)
		if f.keys[k] == nil {
			f.keys[k] = map[string]Value{}
			f.name[k] = parts[i]
		}
	}
}

// SubKeys implements Registry.
func (f *Fake) SubKeys(root Root, path string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := fakeKey(root, path)
	if _, ok := f.keys[k]; !ok {
		return nil, ErrNotExist
	}
	var out []string
	for c := range f.keys {
		if rest, ok := strings.CutPrefix(c, k+`\`); ok && !strings.Contains(rest, `\`) {
			out = append(out, f.name[c])
		}
	}
	slices.Sort(out)
	return out, nil
}

// Values implements Registry.
func (f *Fake) Values(root Root, path string) (map[string]Value, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vals, ok := f.keys[fakeKey(root, path)]
	if !ok {
		return nil, ErrNotExist
	}
	out := make(map[string]Value, len(vals))
	for k, v := range vals {
		out[k] = v
	}
	return out, nil
}

// SetBinary implements Registry.
func (f *Fake) SetBinary(root Root, path, name string, data []byte) error {
	if slices.ContainsFunc(f.Denied, func(d string) bool { return strings.EqualFold(d, root.String()+`\`+path) }) {
		return errors.New("access is denied")
	}
	f.Set(root, path, name, Value{Type: TypeBinary, Bin: slices.Clone(data)})
	f.mu.Lock()
	f.Writes++
	f.mu.Unlock()
	return nil
}
