package brain

import "testing"

// TestExtractAfterWakeWord verifies that extractAfterWakeWord returns the
// command after the wake word, and — crucially — returns an empty string
// when the user only repeated the name without giving a command (so the
// leftover name is never sent to the LLM as a meaningless instruction).
func TestExtractAfterWakeWord(t *testing.T) {
	const wake = "小然"

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"with command after comma", "小然，今天天气怎么样？", "今天天气怎么样？"},
		{"with command no comma", "小然今天天气怎么样", "今天天气怎么样"},
		{"bare name", "小然", ""},
		{"repeated name with period", "小然小然。", ""},
		{"repeated name with comma", "小然，小然！", ""},
		{"name plus particle only", "小然呀", ""},
		{"name plus question", "小然？", ""},
		{"command with trailing particle", "小然，走吧", "走吧"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAfterWakeWord(tc.in, wake)
			if got != tc.want {
				t.Errorf("extractAfterWakeWord(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
