package skills

import "path/filepath"

// Agents and where they read user-level Agent Skills (<dir>/<name>/SKILL.md).
// Researched 2026-11: Claude Code reads ~/.claude/skills; Codex reads the
// shared ~/.agents/skills (also read by Junie, Kimi Code, Gemini CLI,
// Copilot CLI, Cursor, OpenCode); the others also have their own folder.
var Agents = []Agent{
	{
		ID: "claude", Name: "Claude Code", Nick: "claude",
		Note: "run /reload-skills in a running session if ~/.claude/skills is new",
		SkillsDir: func(home string) string {
			return filepath.Join(envOr("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")), "skills")
		},
		Detect: func(home string) bool {
			return exists(filepath.Join(home, ".claude")) || exists(filepath.Join(home, ".claude.json")) || onPath("claude")
		},
	},
	{
		ID: "codex", Name: "OpenAI Codex", Nick: "codex",
		Note:      "shared ~/.agents/skills folder (also read by Junie, Kimi Code, Gemini, Copilot, Cursor, OpenCode)",
		SkillsDir: func(home string) string { return filepath.Join(home, ".agents", "skills") },
		Detect: func(home string) bool {
			return exists(filepath.Join(envOr("CODEX_HOME", filepath.Join(home, ".codex")))) || onPath("codex")
		},
	},
	{
		ID: "junie", Name: "JetBrains Junie", Nick: "junie",
		Note:      "Junie CLI; the IDE plugin's skills folder is not documented",
		SkillsDir: func(home string) string { return filepath.Join(home, ".junie", "skills") },
		Detect: func(home string) bool {
			return exists(filepath.Join(home, ".junie")) || onPath("junie")
		},
	},
	{
		ID: "kimi", Name: "Kimi Code", Nick: "kimi",
		SkillsDir: func(home string) string {
			newHome := envOr("KIMI_CODE_HOME", filepath.Join(home, ".kimi-code"))
			if !exists(newHome) && exists(filepath.Join(home, ".kimi")) {
				return filepath.Join(home, ".kimi", "skills") // legacy kimi-cli
			}
			return filepath.Join(newHome, "skills")
		},
		Detect: func(home string) bool {
			return exists(envOr("KIMI_CODE_HOME", filepath.Join(home, ".kimi-code"))) || exists(filepath.Join(home, ".kimi")) || onPath("kimi")
		},
	},
	{
		ID: "gemini", Name: "Gemini CLI", Nick: "gemini",
		Note:      "may need experimental.skills enabled in ~/.gemini/settings.json",
		SkillsDir: func(home string) string { return filepath.Join(home, ".gemini", "skills") },
		Detect:    func(home string) bool { return exists(filepath.Join(home, ".gemini")) || onPath("gemini") },
	},
	{
		ID: "copilot", Name: "GitHub Copilot CLI", Nick: "copilot",
		SkillsDir: func(home string) string { return filepath.Join(home, ".copilot", "skills") },
		Detect:    func(home string) bool { return exists(filepath.Join(home, ".copilot")) || onPath("copilot") },
	},
	{
		ID: "cursor", Name: "Cursor", Nick: "cursor",
		SkillsDir: func(home string) string { return filepath.Join(home, ".cursor", "skills") },
		Detect:    func(home string) bool { return exists(filepath.Join(home, ".cursor")) || onPath("cursor") },
	},
	{
		ID: "opencode", Name: "OpenCode", Nick: "opencode",
		SkillsDir: func(home string) string { return filepath.Join(home, ".config", "opencode", "skills") },
		Detect: func(home string) bool {
			return exists(filepath.Join(home, ".config", "opencode")) || onPath("opencode")
		},
	},
}
