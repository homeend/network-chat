// Package skills installs a "netchat" Agent Skill (SKILL.md) into the
// user-level configuration of common coding agents, so they know how to use
// the netchat CLI. It only ever touches files it wrote itself (marker:
// "generator: netchat" in the frontmatter).
package skills

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"text/template"
)

// Version of the skill text. Bump when skill.md.tmpl changes meaningfully;
// installed copies with different content are reported as outdated anyway.
const Version = "2"

const (
	skillName = "netchat"
	marker    = "generator: netchat"
)

//go:embed skill.md.tmpl
var tmplText string

var tmpl = template.Must(template.New("skill").Parse(tmplText))

// Agent describes where one coding agent reads user-level skills.
type Agent struct {
	ID   string
	Name string
	Nick string // suggested identity nick for this agent
	Note string
	// SkillsDir returns the user-level skills directory (each skill is <dir>/<name>/SKILL.md).
	SkillsDir func(home string) string
	// Detect reports whether the agent seems installed.
	Detect func(home string) bool
}

// Status is the API view of one agent.
type Status struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Detected         bool   `json:"detected"`
	Note             string `json:"note,omitempty"`
	Path             string `json:"path"`
	Status           string `json:"status"` // missing | current | outdated
	InstalledVersion string `json:"installedVersion,omitempty"`
}

// Overview is GET /v1/skills.
type Overview struct {
	Version string   `json:"version"`
	Content string   `json:"content"` // preview (generic nick)
	Agents  []Status `json:"agents"`
}

var ErrUnknownAgent = errors.New("unknown agent")
var ErrForeign = errors.New("a different skill with this name exists; not touching it")

// Manager resolves paths relative to a home directory (tests use a temp dir).
type Manager struct {
	Home   string
	Binary string // absolute path of the netchat executable, written into the skill
}

func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "netchat"
	} else if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return &Manager{Home: home, Binary: exe}, nil
}

func (m *Manager) skillPath(a Agent) string {
	return filepath.Join(a.SkillsDir(m.Home), skillName, "SKILL.md")
}

// Content renders the skill for an agent.
func (m *Manager) Content(nick string) string {
	var b bytes.Buffer
	_ = tmpl.Execute(&b, map[string]string{"Version": Version, "Binary": m.Binary, "AgentNick": nick})
	return b.String()
}

var versionRe = regexp.MustCompile(`(?m)^\s*version:\s*"?([^"\r\n]+)"?\s*$`)

func (m *Manager) status(a Agent) Status {
	s := Status{ID: a.ID, Name: a.Name, Note: a.Note, Detected: a.Detect(m.Home), Path: m.skillPath(a), Status: "missing"}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return s
	}
	text := string(b)
	if !strings.Contains(text, marker) {
		s.Status = "foreign"
		return s
	}
	if v := versionRe.FindStringSubmatch(text); v != nil {
		s.InstalledVersion = v[1]
	}
	if normalize(text) == normalize(m.Content(a.Nick)) {
		s.Status = "current"
	} else {
		s.Status = "outdated" // older version, or the netchat binary moved
	}
	return s
}

func normalize(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func (m *Manager) Overview() Overview {
	o := Overview{Version: Version, Content: m.Content("<your-agent-nick>"), Agents: make([]Status, len(Agents))}
	var wg sync.WaitGroup // PATH lookups are slow on Windows: detect in parallel
	for i, a := range Agents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o.Agents[i] = m.status(a)
		}()
	}
	wg.Wait()
	return o
}

func find(id string) (Agent, error) {
	for _, a := range Agents {
		if a.ID == id {
			return a, nil
		}
	}
	return Agent{}, fmt.Errorf("%w: %s", ErrUnknownAgent, id)
}

// Install writes (or updates) the skill for one agent.
func (m *Manager) Install(id string) (Status, error) {
	a, err := find(id)
	if err != nil {
		return Status{}, err
	}
	if st := m.status(a); st.Status == "foreign" {
		return st, fmt.Errorf("%w: %s", ErrForeign, st.Path)
	}
	path := m.skillPath(a)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(m.Content(a.Nick)), 0o644); err != nil {
		return Status{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return Status{}, err
	}
	return m.status(a), nil
}

// Remove deletes the skill we installed for one agent.
func (m *Manager) Remove(id string) (Status, error) {
	a, err := find(id)
	if err != nil {
		return Status{}, err
	}
	st := m.status(a)
	switch st.Status {
	case "missing":
		return st, nil
	case "foreign":
		return st, fmt.Errorf("%w: %s", ErrForeign, st.Path)
	}
	if err := os.Remove(st.Path); err != nil {
		return st, err
	}
	_ = os.Remove(filepath.Dir(st.Path)) // only succeeds if empty
	return m.status(a), nil
}

// ---- detection helpers ----

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// lookPath is replaceable in tests (PATH scans are slow on Windows).
var lookPath = exec.LookPath

func onPath(names ...string) bool {
	for _, n := range names {
		if _, err := lookPath(n); err == nil {
			return true
		}
	}
	return false
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
