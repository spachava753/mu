package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestQuoteBlock(t *testing.T) {
	for name, test := range map[string]struct {
		parts []ui.SelectedText
		want  string
	}{
		"nothing":    {nil, ""},
		"whitespace": {[]ui.SelectedText{{Text: "a  b", Start: 1, End: 3}}, ""},
		"runes":      {[]ui.SelectedText{{Text: "héllo wörld", Start: 1, End: 8}}, "> éllo wö"},
		"paragraphs": {[]ui.SelectedText{{Text: "first", Start: 2, End: 5}, {Text: "second", Start: 0, End: 3}}, "> rst\n>\n> sec"},
		"lines":      {[]ui.SelectedText{{Text: "if x {\n\n\ty()  \n}\n", Start: 0, End: 18}}, "> if x {\n>\n> \ty()\n> }"},
		"empty end":  {[]ui.SelectedText{{Text: "first", Start: 0, End: 5}, {Text: "second", Start: 0, End: 0}}, "> first"},
		"past end":   {[]ui.SelectedText{{Text: "short", Start: 3, End: 90}}, "> rt"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := quoteBlock(test.parts); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestAppendQuote(t *testing.T) {
	for _, test := range []struct{ draft, want string }{
		{"", "> q\n\n"},
		{"my question", "my question\n> q\n\n"},
		{"my question\n", "my question\n> q\n\n"},
		{"> p\n\n", "> p\n\n> q\n\n"},
	} {
		if got := appendQuote(test.draft, "> q"); got != test.want {
			t.Fatalf("draft %q: got %q, want %q", test.draft, got, test.want)
		}
	}
}
