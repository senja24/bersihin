//go:build windows

package winreg

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// System is the registry of this machine. Reads open keys with
// KEY_READ only; SetBinary opens one key with KEY_SET_VALUE and never asks
// for elevation, so a key the user may not write fails.
type System struct{}

func hive(r Root) registry.Key {
	if r == LocalMachine {
		return registry.LOCAL_MACHINE
	}
	return registry.CURRENT_USER
}

func open(root Root, path string, access uint32) (registry.Key, error) {
	k, err := registry.OpenKey(hive(root), path, access)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, ErrNotExist
	}
	return k, err
}

// SubKeys implements Registry.
func (System) SubKeys(root Root, path string) ([]string, error) {
	k, err := open(root, path, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	return k.ReadSubKeyNames(-1)
}

// Values implements Registry. Values of other types, and values that cannot
// be read, are left out.
func (System) Values(root Root, path string) (map[string]Value, error) {
	k, err := open(root, path, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	names, err := k.ReadValueNames(-1)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Value, len(names))
	for _, n := range names {
		_, typ, err := k.GetValue(n, nil)
		if err != nil && !errors.Is(err, registry.ErrShortBuffer) {
			continue
		}
		switch typ {
		case registry.SZ, registry.EXPAND_SZ:
			if s, _, err := k.GetStringValue(n); err == nil {
				out[n] = Value{Type: typ, Str: s}
			}
		case registry.DWORD, registry.QWORD:
			if v, _, err := k.GetIntegerValue(n); err == nil {
				out[n] = Value{Type: typ, Num: v}
			}
		case registry.BINARY:
			if b, _, err := k.GetBinaryValue(n); err == nil {
				out[n] = Value{Type: typ, Bin: b}
			}
		}
	}
	return out, nil
}

// SetBinary implements Registry.
func (System) SetBinary(root Root, path, name string, data []byte) error {
	k, _, err := registry.CreateKey(hive(root), path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetBinaryValue(name, data)
}
