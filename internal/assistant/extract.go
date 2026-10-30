package assistant

import (
	"encoding/json"
	"errors"
	"strings"
)

// maxExtract caps the text that extractJSON looks at.
const maxExtract = 1 << 20

// maxCandidates is how many balanced {...} pieces are tried before giving up.
const maxCandidates = 32

var errNoJSON = errors.New("no JSON object in the answer")

// extractJSON pulls the answer object out of what a compatible server sent
// back. Such servers often wrap it: a leading <think>...</think> block, a code
// fence, or a sentence before and after. It returns the first balanced,
// valid JSON object; a top-level array, a truncated object and empty text are
// errors. Nothing here is trusted: the caller decodes the result strictly.
func extractJSON(text string) (string, error) {
	if len(text) > maxExtract {
		return "", errNoJSON
	}
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "\ufeff"))
	t = stripThink(t)
	if t == "" {
		return "", errNoJSON
	}
	if strings.HasPrefix(t, "```") {
		if i := strings.IndexByte(t, '\n'); i >= 0 {
			t = t[i+1:]
		} else {
			return "", errNoJSON
		}
		if i := strings.LastIndex(t, "```"); i >= 0 {
			t = t[:i]
		}
		t = strings.TrimSpace(t)
	}
	if strings.HasPrefix(t, "[") {
		return "", errNoJSON
	}
	// Each candidate is scanned once and the search resumes after it, so the
	// work is linear in len(t); the number of candidates is capped as well.
	for from, tries := 0, 0; from < len(t) && tries < maxCandidates; tries++ {
		i := strings.IndexByte(t[from:], '{')
		if i < 0 {
			break
		}
		start := from + i
		end := balancedEnd(t, start)
		if end < 0 {
			return "", errNoJSON // truncated: an inner object would be the wrong one
		}
		if cand := t[start:end]; json.Valid([]byte(cand)) {
			return cand, nil
		}
		from = end
	}
	return "", errNoJSON
}

// stripThink removes a leading <think>...</think> block (reasoning models).
// An opening tag without a closing one means the answer never came.
func stripThink(t string) string {
	const open, closeTag = "<think>", "</think>"
	if !strings.HasPrefix(t, open) {
		return t
	}
	i := strings.Index(t, closeTag)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(t[i+len(closeTag):])
}

// balancedEnd returns the index just past the object that starts at t[start],
// or -1 when it is not closed. Braces inside strings do not count.
func balancedEnd(t string, start int) int {
	depth := 0
	inStr, esc := false, false
	for i := start; i < len(t); i++ {
		c := t[i]
		switch {
		case inStr:
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// extractAdvice extracts the object and checks its top-level keys: only
// "answer" and "suggestions" are expected, so an unrelated object found in the
// text (an example, a tool call) is not mistaken for the answer.
func extractAdvice(text string) ([]byte, error) {
	obj, err := extractJSON(text)
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(obj), &top); err != nil {
		return nil, errNoJSON
	}
	for k := range top {
		if k != "answer" && k != "suggestions" {
			return nil, errors.New("unexpected key in the answer")
		}
	}
	return []byte(obj), nil
}
