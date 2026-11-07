// Package agents teaches coding agents how to use AirBuild inside a project.
//
// Three pieces cooperate:
//
//   - A small always-on block in AGENTS.md / CLAUDE.md (safety rules + a
//     pointer to the skills), stamped with the CLI version.
//   - Two thin skills (airbuild-distribution, airbuild-codepush) whose only
//     job is to say when to load and to run `airbuild guide <topic>`.
//   - The guides themselves, embedded in the CLI binary, so the real runbooks
//     always match the installed CLI version instead of going stale in a
//     client repo.
package agents

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Markers delimit the AirBuild-managed region of AGENTS.md / CLAUDE.md so
// re-running `airbuild init --agents` or `airbuild agents sync` replaces it
// in place instead of appending a duplicate. The start marker carries the CLI
// version that wrote it (`<!-- airbuild:start v=1.2.0 -->`).
const (
	startPrefix = "<!-- airbuild:start"
	endMarker   = "<!-- airbuild:end -->"
	skillMarker = "<!-- airbuild:skill"
)

// Files are the agent instruction files that receive the core block. AGENTS.md
// is the emerging cross-tool standard; CLAUDE.md is kept in sync for tools that
// only read it.
var Files = []string{"AGENTS.md", "CLAUDE.md"}

// DefaultServer is the hosted AirBuild origin, used when the caller doesn't
// have a configured API URL.
const DefaultServer = "https://airbuild.dev"

//go:embed content/*.md
var content embed.FS

// Topic is a guide printed by `airbuild guide <name>`.
type Topic struct {
	Name    string
	Summary string
	file    string
}

// Topics are the available guides, in display order.
var Topics = []Topic{
	{"distribution", "Upload builds, install links/QR, iOS UDIDs, in-app SDKs, CI, webhooks", "distribution.md"},
	{"codepush", "OTA shared rules: patch vs new binary, lifecycle, channels, rollout, auto-pause", "codepush.md"},
	{"flutter", "Flutter CodePush runbook (Android + iOS)", "flutter.md"},
	{"react-native", "React Native CodePush runbook (Expo Updates, Android + iOS)", "react-native.md"},
}

// Skill is a thin skill file written to each agent skill directory.
type Skill struct {
	Name string
	file string
}

// Skills are the skills we maintain.
var Skills = []Skill{
	{"airbuild-distribution", "skill-distribution.md"},
	{"airbuild-codepush", "skill-codepush.md"},
}

// skillRoots are the per-tool skill directories. `.agents/skills` is the
// shared default and is always written; the others only if the tool's config
// directory already exists in the project (so we don't litter repos).
var skillRoots = []struct{ root, toolDir string }{
	{filepath.Join(".agents", "skills"), ""},
	{filepath.Join(".claude", "skills"), ".claude"},
	{filepath.Join(".devin", "skills"), ".devin"},
	{filepath.Join(".windsurf", "skills"), ".windsurf"},
}

func normalizeServer(serverBase string) string {
	base := strings.TrimSuffix(serverBase, "/")
	if base == "" {
		return DefaultServer
	}
	return base
}

func render(file, serverBase, version string) string {
	b, err := content.ReadFile("content/" + file)
	if err != nil {
		panic(fmt.Sprintf("agents: missing embedded %s: %v", file, err))
	}
	if version == "" {
		version = "dev"
	}
	r := strings.NewReplacer("__SERVER__", normalizeServer(serverBase), "__VERSION__", version)
	return r.Replace(string(b))
}

// Guide returns the rendered guide for a topic, or false if unknown.
func Guide(topic, serverBase string) (string, bool) {
	for _, t := range Topics {
		if t.Name == topic {
			return render(t.file, serverBase, ""), true
		}
	}
	return "", false
}

// block builds the core text written between the markers.
func block(serverBase, version string) string {
	if version == "" {
		version = "dev"
	}
	return startPrefix + " v=" + version + " -->\n" + render("core.md", serverBase, version) + endMarker
}

var versionRe = regexp.MustCompile(`v=(\S+)`)

// findBlock locates the managed region. It matches both the versioned and the
// legacy unversioned start marker. version is "" for the legacy form.
func findBlock(s string) (start, end int, version string, ok bool) {
	start = strings.Index(s, startPrefix)
	if start < 0 {
		return 0, 0, "", false
	}
	e := strings.Index(s[start:], endMarker)
	if e < 0 {
		return 0, 0, "", false
	}
	end = start + e + len(endMarker)
	line := s[start:]
	if nl := strings.Index(line, "-->"); nl >= 0 {
		line = line[:nl]
	}
	if m := versionRe.FindStringSubmatch(line); m != nil {
		version = m[1]
	}
	return start, end, version, true
}

// WriteAll writes the core block into every file in Files plus the skills into
// `.agents/skills` (and into any tool skill directory whose parent exists).
// Existing content is preserved. Returns the paths written.
func WriteAll(dir, serverBase, version string) ([]string, error) {
	var written []string
	for _, name := range Files {
		path := filepath.Join(dir, name)
		if err := upsertBlock(path, serverBase, version, true); err != nil {
			return written, fmt.Errorf("writing %s: %w", name, err)
		}
		written = append(written, path)
	}
	for _, r := range skillRoots {
		if r.toolDir != "" {
			if fi, err := os.Stat(filepath.Join(dir, r.toolDir)); err != nil || !fi.IsDir() {
				continue
			}
		}
		for _, sk := range Skills {
			path := filepath.Join(dir, r.root, sk.Name, "SKILL.md")
			wrote, err := upsertSkill(path, sk, serverBase, version, true)
			if err != nil {
				return written, fmt.Errorf("writing %s: %w", path, err)
			}
			if wrote {
				written = append(written, path)
			}
		}
	}
	return written, nil
}

// Sync refreshes only files AirBuild already manages in dir; it never creates
// files. Returns the paths whose content changed.
func Sync(dir, serverBase, version string) ([]string, error) {
	var updated []string
	for _, name := range Files {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if _, _, _, ok := findBlock(string(b)); !ok {
			continue
		}
		before := string(b)
		if err := upsertBlock(path, serverBase, version, false); err != nil {
			return updated, err
		}
		if after, _ := os.ReadFile(path); string(after) != before {
			updated = append(updated, path)
		}
	}
	for _, r := range skillRoots {
		for _, sk := range Skills {
			path := filepath.Join(dir, r.root, sk.Name, "SKILL.md")
			wrote, err := upsertSkill(path, sk, serverBase, version, false)
			if err != nil {
				return updated, err
			}
			if wrote {
				updated = append(updated, path)
			}
		}
	}
	return updated, nil
}

// Finding is one managed file found by Check.
type Finding struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"` // "block" or "skill"
	Version string `json:"version"`
	Stale   bool   `json:"stale"`
}

// Check reports every AirBuild-managed file in dir and whether it was written
// by an older CLI than `version`. Development builds ("dev") never mark a
// stamped file stale, but unstamped (legacy) files are always stale.
func Check(dir, version string) ([]Finding, error) {
	var out []Finding
	stale := func(found string) bool {
		if found == "" {
			return true
		}
		return version != "" && version != "dev" && found != version
	}
	for _, name := range Files {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if _, _, v, ok := findBlock(string(b)); ok {
			out = append(out, Finding{path, "block", v, stale(v)})
		}
	}
	for _, r := range skillRoots {
		for _, sk := range Skills {
			path := filepath.Join(dir, r.root, sk.Name, "SKILL.md")
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			s := string(b)
			i := strings.Index(s, skillMarker)
			if i < 0 {
				continue
			}
			line := s[i:]
			if nl := strings.Index(line, "-->"); nl >= 0 {
				line = line[:nl]
			}
			v := ""
			if m := versionRe.FindStringSubmatch(line); m != nil {
				v = m[1]
			}
			out = append(out, Finding{path, "skill", v, stale(v)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func upsertBlock(path, serverBase, version string, create bool) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	cur := string(existing)
	next := block(serverBase, version)

	var out string
	switch {
	case err != nil:
		if !create {
			return nil
		}
		out = "# Project — AirBuild setup\n\n" + next + "\n"
	default:
		if start, end, _, ok := findBlock(cur); ok {
			out = cur[:start] + next + cur[end:]
		} else if create {
			sep := "\n\n"
			if strings.HasSuffix(cur, "\n") {
				sep = "\n"
			}
			out = cur + sep + next + "\n"
		} else {
			return nil
		}
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

// upsertSkill writes a skill file. Skills we wrote carry skillMarker and are
// overwritten; a file at the same path without the marker is the user's own
// and is left alone. Returns true when the file was created or changed.
func upsertSkill(path string, sk Skill, serverBase, version string, create bool) (bool, error) {
	next := render(sk.file, serverBase, version)
	existing, err := os.ReadFile(path)
	switch {
	case err != nil && !os.IsNotExist(err):
		return false, err
	case err != nil:
		if !create {
			return false, nil
		}
	default:
		if !strings.Contains(string(existing), skillMarker) {
			return false, nil
		}
		if string(existing) == next {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(next), 0o644)
}
