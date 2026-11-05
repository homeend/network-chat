package node

import "testing"

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		"report.pdf":         "report.pdf",
		"../../etc/passwd":   "passwd",
		`..\..\win\evil.exe`: "evil.exe",
		"CON.txt":            "_CON.txt",
		"com1":               "_com1",
		"a<b>c:d|e?.txt":     "a_b_c_d_e_.txt",
		"  .hidden. ":        "hidden",
		"":                   "file",
		"..":                 "file",
		"tab\there\x00.txt":  "tab_here_.txt",
		"ok name (1).tar.gz": "ok name (1).tar.gz",
	}
	for in, want := range cases {
		if got := SafeFileName(in); got != want {
			t.Errorf("SafeFileName(%q) = %q, want %q", in, got, want)
		}
	}
	long := SafeFileName(string(make([]byte, 0)) + repeat("a", 300) + ".txt")
	if len(long) != 200 || long[len(long)-4:] != ".txt" {
		t.Errorf("long name not truncated with extension kept: %d %q", len(long), long[len(long)-4:])
	}
}

func repeat(s string, n int) string {
	out := ""
	for range n {
		out += s
	}
	return out
}

func TestMentions(t *testing.T) {
	yes := []string{"@claude hi", "hey @claude", "hey @Claude, check", "(@claude)", "ping @claude."}
	no := []string{"claude", "@claudeX", "mail@claude", "@claude_bot", "@claude-2", "@@claude"}
	for _, s := range yes {
		if !mentions(s, "claude") {
			t.Errorf("expected mention in %q", s)
		}
	}
	for _, s := range no {
		if mentions(s, "claude") {
			t.Errorf("unexpected mention in %q", s)
		}
	}
}
