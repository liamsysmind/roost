package session

import (
	"strings"
	"testing"
)

// The id becomes a tmux session name and a log filename, so what it may
// contain is a safety question, not a stylistic one. It used to be an ASCII
// whitelist; these cases pin down what replaced it.
func TestValidateID(t *testing.T) {
	cases := []struct {
		name string
		id   string
		ok   bool
		why  string
	}{
		{"plain ascii", "RoundBase", true, ""},
		{"with dash and underscore", "round-base_2", true, ""},
		{"han", "病歷系統", true, "tmux stores it verbatim"},
		{"han mixed with ascii", "RoundBase-測試", true, ""},
		{"kana", "スクリーン", true, ""},
		{"hangul", "사진", true, ""},

		{"empty", "", false, "nothing to name"},
		{"slash", "a/b", false, "a separator is a way out of the log directory"},
		{"backslash", `a\b`, false, ""},
		{"parent", "..", false, ""},
		{"dot alone", ".", false, ""},
		// tmux rewrites both to '_' and then "-t =id" never matches again.
		{"dot inside", "v1.2", false, "tmux rewrites . to _"},
		{"colon inside", "a:b", false, "tmux rewrites : to _"},
		{"space", "round base", false, ""},
		{"ideographic space", "round\u3000base", false, ""},
		{"tab", "a\tb", false, ""},
		{"control char", "a\x01b", false, ""},
		{"zero width space", "a\u200bb", false, "renders identically to \"ab\""},
		{"bidi override", "a\u202eb", false, ""},
		{"bom", "a\ufeffb", false, ""},

		{"128 runes", strings.Repeat("a", 128), true, ""},
		{"129 runes", strings.Repeat("a", 129), false, ""},
		// 67 Han runes is 201 bytes: under the rune cap, over the byte cap that
		// a filesystem's per-name limit actually enforces.
		{"over byte cap", strings.Repeat("病", 67), false, "201 bytes"},
		{"under byte cap", strings.Repeat("病", 66), true, "198 bytes"},
	}

	for _, c := range cases {
		err := ValidateID(c.id)
		if c.ok && err != nil {
			t.Errorf("%s: ValidateID(%q) = %v, want accepted (%s)", c.name, c.id, err, c.why)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: ValidateID(%q) accepted, want rejected (%s)", c.name, c.id, c.why)
		}
	}
}
