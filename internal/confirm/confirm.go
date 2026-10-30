// Package confirm issues the single-use confirmation tokens that destructive
// calls must present. A token is bound to one action, the scan the items came
// from and the exact set of items, so a token cannot be reused for anything
// else. The page asks for a token only after the user has confirmed.
package confirm

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"slices"
	"sync"
	"time"

	"dev.agus.bersihin/internal/config"
)

// The page matches these texts (src/lib/confirm.ts); none may contain "cancel".
var (
	// ErrRequired: the token is missing, unknown, already used or for something else.
	ErrRequired = errors.New("Confirmation required")
	// ErrExpired: the token was valid but is too old.
	ErrExpired = errors.New("Confirmation expired")
)

type grant struct {
	action, scanID string
	digest         [sha256.Size]byte
	expires        time.Time
	issued         time.Time
}

// Store holds the outstanding tokens. The zero value works (real clock).
type Store struct {
	// Now is the clock; default time.Now.
	Now func() time.Time

	mu     sync.Mutex
	grants map[string]grant
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// digest hashes the sorted, de-duplicated subjects, each prefixed by its
// length so that no two different sets hash alike. Subjects are compared as
// exact strings: no case or Unicode folding.
func digest(subjects []string) [sha256.Size]byte {
	list := slices.Clone(subjects)
	slices.Sort(list)
	list = slices.Compact(list)
	h := sha256.New()
	var n [8]byte
	for _, v := range list {
		binary.BigEndian.PutUint64(n[:], uint64(len(v)))
		h.Write(n[:])
		h.Write([]byte(v))
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// purge drops expired grants and, when full, the oldest. The caller holds mu.
func (s *Store) purge(now time.Time, room int) {
	for k, g := range s.grants {
		if !now.Before(g.expires) {
			delete(s.grants, k)
		}
	}
	for len(s.grants) > 0 && len(s.grants) > config.MaxGrants-room {
		var oldest string
		var at time.Time
		for k, g := range s.grants {
			if oldest == "" || g.issued.Before(at) {
				oldest, at = k, g.issued
			}
		}
		delete(s.grants, oldest)
	}
}

// Mint returns a token for action on exactly these subjects of the scan.
func (s *Store) Mint(action, scanID string, subjects []string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b[:])
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grants == nil {
		s.grants = map[string]grant{}
	}
	s.purge(now, 1)
	s.grants[tok] = grant{
		action: action, scanID: scanID, digest: digest(subjects),
		issued: now, expires: now.Add(config.ConfirmTTL),
	}
	return tok, nil
}

// Redeem checks the token against the request and uses it up. Any lookup that
// finds the token burns it, whether it matches or not.
func (s *Store) Redeem(token, action, scanID string, subjects []string) error {
	now := s.now()
	s.mu.Lock()
	g, ok := s.grants[token]
	delete(s.grants, token)
	s.purge(now, 0)
	s.mu.Unlock()
	if !ok || token == "" {
		return ErrRequired
	}
	if !now.Before(g.expires) {
		return ErrExpired
	}
	want := digest(subjects)
	if g.action != action || g.scanID != scanID || subtle.ConstantTimeCompare(g.digest[:], want[:]) != 1 {
		return ErrRequired
	}
	return nil
}

// Pending reports whether an unexpired token for this scan id is outstanding.
func (s *Store) Pending(scanID string) bool {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.grants {
		if g.scanID == scanID && now.Before(g.expires) {
			return true
		}
	}
	return false
}
