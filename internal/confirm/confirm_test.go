package confirm

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newStore() (*Store, *clock) {
	c := &clock{t: time.Unix(1_000_000, 0)}
	return &Store{Now: c.now}, c
}

func TestRedeemMatchingToken(t *testing.T) {
	s, _ := newStore()
	tok, err := s.Mint("clean", "scan1", []string{"a", "b"})
	if err != nil || tok == "" {
		t.Fatalf("mint: %q %v", tok, err)
	}
	// the same set in another order, with a duplicate, is the same set
	if err := s.Redeem(tok, "clean", "scan1", []string{"b", "a", "b"}); err != nil {
		t.Fatalf("redeem: %v", err)
	}
}

func TestRedeemRefusals(t *testing.T) {
	cases := []struct {
		name     string
		action   string
		scan     string
		subjects []string
	}{
		{"wrong action", "trash", "scan1", []string{"a"}},
		{"wrong scan", "clean", "scan2", []string{"a"}},
		{"extra id", "clean", "scan1", []string{"a", "b"}},
		{"missing id", "clean", "scan1", []string{}},
		{"different id", "clean", "scan1", []string{"b"}},
		{"case differs", "clean", "scan1", []string{"A"}},
		{"joined ids cannot collide", "clean", "scan1", []string{"a\x00"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := newStore()
			tok, _ := s.Mint("clean", "scan1", []string{"a"})
			err := s.Redeem(tok, c.action, c.scan, c.subjects)
			if !errors.Is(err, ErrRequired) {
				t.Fatalf("err = %v, want ErrRequired", err)
			}
			// a mismatch burns the token: the right request now fails too
			if err := s.Redeem(tok, "clean", "scan1", []string{"a"}); !errors.Is(err, ErrRequired) {
				t.Fatalf("token survived a mismatch: %v", err)
			}
		})
	}
}

func TestLengthPrefixPreventsCollisions(t *testing.T) {
	s, _ := newStore()
	tok, _ := s.Mint("trash", "s", []string{"ab", "c"})
	if err := s.Redeem(tok, "trash", "s", []string{"a", "bc"}); !errors.Is(err, ErrRequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestEmptyAndUnknownTokens(t *testing.T) {
	s, _ := newStore()
	for _, tok := range []string{"", "nope", "AAAA"} {
		if err := s.Redeem(tok, "clean", "s", []string{"a"}); !errors.Is(err, ErrRequired) {
			t.Errorf("Redeem(%q) = %v", tok, err)
		}
	}
}

func TestSingleUse(t *testing.T) {
	s, _ := newStore()
	tok, _ := s.Mint("clean", "s", []string{"a"})
	if err := s.Redeem(tok, "clean", "s", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Redeem(tok, "clean", "s", []string{"a"}); !errors.Is(err, ErrRequired) {
		t.Fatalf("second use: %v", err)
	}
}

func TestExpiry(t *testing.T) {
	s, c := newStore()
	tok, _ := s.Mint("clean", "s", []string{"a"})
	c.t = c.t.Add(2*time.Minute - time.Second)
	if err := s.Redeem(tok, "clean", "s", []string{"a"}); err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
	tok, _ = s.Mint("clean", "s", []string{"a"})
	c.t = c.t.Add(2 * time.Minute)
	if err := s.Redeem(tok, "clean", "s", []string{"a"}); !errors.Is(err, ErrExpired) {
		t.Fatalf("at 2 minutes: %v", err)
	}
	// and it is gone afterwards
	if err := s.Redeem(tok, "clean", "s", []string{"a"}); !errors.Is(err, ErrRequired) {
		t.Fatalf("after expiry: %v", err)
	}
}

func TestSixtyFifthGrantEvictsTheOldest(t *testing.T) {
	s, c := newStore()
	var toks []string
	for i := range 65 {
		c.t = c.t.Add(time.Millisecond)
		tok, err := s.Mint("clean", "s", []string{string(rune('a' + i))})
		if err != nil {
			t.Fatal(err)
		}
		toks = append(toks, tok)
	}
	if err := s.Redeem(toks[0], "clean", "s", []string{"a"}); !errors.Is(err, ErrRequired) {
		t.Fatalf("oldest survived: %v", err)
	}
	if err := s.Redeem(toks[64], "clean", "s", []string{string(rune('a' + 64))}); err != nil {
		t.Fatalf("newest: %v", err)
	}
	if err := s.Redeem(toks[1], "clean", "s", []string{"b"}); err != nil {
		t.Fatalf("second oldest: %v", err)
	}
}

func TestConcurrentRedeemOneWins(t *testing.T) {
	s, _ := newStore()
	tok, _ := s.Mint("clean", "s", []string{"a"})
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Redeem(tok, "clean", "s", []string{"a"}) == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d redeems won", wins.Load())
	}
}

func TestErrorTexts(t *testing.T) {
	if ErrRequired.Error() != "Confirmation required" || ErrExpired.Error() != "Confirmation expired" {
		t.Fatalf("texts changed: %q %q", ErrRequired, ErrExpired)
	}
}

func TestPendingSeesLiveGrantsOfAScan(t *testing.T) {
	s, c := newStore()
	if s.Pending("scan1") {
		t.Fatal("nothing minted yet")
	}
	tok, _ := s.Mint("clean", "scan1", []string{"a"})
	if !s.Pending("scan1") || s.Pending("scan2") {
		t.Fatal("Pending is wrong for a live grant")
	}
	s.Redeem(tok, "clean", "scan1", []string{"a"})
	if s.Pending("scan1") {
		t.Fatal("a used grant is not pending")
	}
	s.Mint("clean", "scan1", []string{"a"})
	c.t = c.t.Add(2 * time.Minute)
	if s.Pending("scan1") {
		t.Fatal("an expired grant is not pending")
	}
}
