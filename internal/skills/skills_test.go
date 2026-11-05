package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "KIMI_CODE_HOME"} {
		t.Setenv(v, "")
	}
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	return &Manager{Home: t.TempDir(), Binary: "/opt/netchat/netchat"}
}

func statusOf(m *Manager, id string) Status {
	for _, s := range m.Overview().Agents {
		if s.ID == id {
			return s
		}
	}
	return Status{}
}

func TestInstallUpdateRemove(t *testing.T) {
	m := testManager(t)
	if s := statusOf(m, "claude"); s.Status != "missing" || !strings.HasPrefix(s.Path, m.Home) {
		t.Fatalf("initial: %+v", s)
	}
	st, err := m.Install("claude")
	if err != nil || st.Status != "current" || st.InstalledVersion != Version {
		t.Fatalf("install: %+v %v", st, err)
	}
	b, _ := os.ReadFile(st.Path)
	if !strings.Contains(string(b), "name: netchat") || !strings.Contains(string(b), "/opt/netchat/netchat") || !strings.Contains(string(b), "id new claude --agent") {
		t.Fatalf("content:\n%s", b)
	}

	// Binary moved -> outdated; Install updates it.
	m.Binary = "/usr/local/bin/netchat"
	if s := statusOf(m, "claude"); s.Status != "outdated" {
		t.Fatalf("after move: %+v", s)
	}
	if st, err := m.Install("claude"); err != nil || st.Status != "current" {
		t.Fatalf("update: %+v %v", st, err)
	}

	// Old version marker -> outdated.
	os.WriteFile(st.Path, []byte(strings.Replace(string(b), `version: "`+Version+`"`, `version: "0"`, 1)), 0o644)
	if s := statusOf(m, "claude"); s.Status != "outdated" || s.InstalledVersion != "0" {
		t.Fatalf("old version: %+v", s)
	}

	if st, err := m.Remove("claude"); err != nil || st.Status != "missing" {
		t.Fatalf("remove: %+v %v", st, err)
	}
	if _, err := os.Stat(filepath.Dir(st.Path)); !os.IsNotExist(err) {
		t.Fatalf("skill dir not removed")
	}
	// Other agents untouched.
	if s := statusOf(m, "codex"); s.Status != "missing" {
		t.Fatalf("codex: %+v", s)
	}
}

func TestForeignSkillIsNeverTouched(t *testing.T) {
	m := testManager(t)
	p := filepath.Join(m.Home, ".junie", "skills", "netchat", "SKILL.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("---\nname: netchat\ndescription: mine\n---\nhand written"), 0o644)
	if s := statusOf(m, "junie"); s.Status != "foreign" {
		t.Fatalf("status: %+v", s)
	}
	if _, err := m.Install("junie"); err == nil {
		t.Fatal("install overwrote a foreign skill")
	}
	if _, err := m.Remove("junie"); err == nil {
		t.Fatal("remove deleted a foreign skill")
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "hand written") {
		t.Fatal("foreign skill changed")
	}
}

func TestSkillFrontmatterFollowsSpec(t *testing.T) {
	c := testManager(t).Content("x")
	if !strings.HasPrefix(c, "---\nname: netchat\ndescription: ") {
		t.Fatalf("frontmatter start:\n%s", c[:80])
	}
	desc := strings.SplitN(strings.SplitN(c, "description: ", 2)[1], "\n", 2)[0]
	if len(desc) == 0 || len(desc) > 1024 {
		t.Fatalf("description length %d", len(desc))
	}
	if _, err := (&Manager{}).Install("nope"); err == nil {
		t.Fatal("unknown agent accepted")
	}
}
