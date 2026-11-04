package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spachava753/mu/internal/state"
)

func count(n uint64) string {
	s := strconv.FormatUint(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
func contextLabel(u *state.Usage) string {
	if u.Size <= 0 {
		return "Context " + count(u.Used) + " / unknown limit"
	}
	return fmt.Sprintf("Context %s / %s (%.1f%%)", count(u.Used), count(u.Size), 100*float64(u.Used)/float64(u.Size))
}

// Drop only a redundant display prefix matching the surrounding field label.
// Protocol values and unrelated labels (including provider/model names) stay intact.
func optionLabel(field, label string) string {
	prefix, rest, ok := strings.Cut(label, ":")
	if ok && strings.EqualFold(strings.TrimSpace(prefix), strings.TrimSpace(field)) && strings.TrimSpace(rest) != "" {
		return strings.TrimSpace(rest)
	}
	return label
}
