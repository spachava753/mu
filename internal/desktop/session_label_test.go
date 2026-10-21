package desktop

import "testing"

func TestOptionLabelsOnlyRemoveRepeatedFieldNames(t *testing.T) {
	for _, tc := range []struct{ field, label, want string }{
		{"Thinking", "Thinking: high", "high"},
		{"Thinking", "thinking: low", "low"},
		{"Thinking", "high", "high"},
		{"Thinking", "Thinking: ", "Thinking: "},
		{"Model", "provider:model", "provider:model"},
		{"Model", "provider/Model: variant", "provider/Model: variant"},
	} {
		if got := optionLabel(tc.field, tc.label); got != tc.want {
			t.Errorf("%q / %q: got %q, want %q", tc.field, tc.label, got, tc.want)
		}
	}
}
