package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testServer  = "https://airbuild.test"
	testVersion = "v1.2.0"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func skillPath(dir, root, name string) string {
	return filepath.Join(dir, root, name, "SKILL.md")
}

func TestWriteAllCreatesCoreBlockAndSkills(t *testing.T) {
	dir := t.TempDir()
	written, err := WriteAll(dir, testServer, testVersion)
	if err != nil {
		t.Fatalf("WriteAll failed: %v", err)
	}
	// 2 core files + 2 skills in .agents/skills (no tool dirs exist).
	if len(written) != len(Files)+len(Skills) {
		t.Fatalf("wrote %d files, want %d: %v", len(written), len(Files)+len(Skills), written)
	}

	for _, name := range Files {
		s := read(t, filepath.Join(dir, name))
		if !strings.Contains(s, startPrefix+" v="+testVersion+" -->") || !strings.Contains(s, endMarker) {
			t.Errorf("%s missing versioned markers", name)
		}
		for _, want := range []string{"AIRBUILD_API_KEY", "--json", "airbuild-distribution", "airbuild-codepush",
			"airbuild guide", testServer + "/api/openapi"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s missing %q", name, want)
			}
		}
		if strings.Contains(s, "__SERVER__") || strings.Contains(s, "__VERSION__") {
			t.Errorf("%s has an unreplaced placeholder", name)
		}
	}

	for _, sk := range Skills {
		s := read(t, skillPath(dir, filepath.Join(".agents", "skills"), sk.Name))
		if !strings.HasPrefix(s, "---\nname: "+sk.Name+"\n") || !strings.Contains(s, "description: Use when") {
			t.Errorf("skill %s has bad frontmatter", sk.Name)
		}
		if !strings.Contains(s, skillMarker+" v="+testVersion+" -->") {
			t.Errorf("skill %s missing version stamp", sk.Name)
		}
		if !strings.Contains(s, "airbuild guide") {
			t.Errorf("skill %s does not point at the guide command", sk.Name)
		}
	}
}

func TestSkillsOnlyWrittenToExistingToolDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteAll(dir, testServer, testVersion); err != nil {
		t.Fatal(err)
	}
	for _, sk := range Skills {
		if _, err := os.Stat(skillPath(dir, filepath.Join(".claude", "skills"), sk.Name)); err != nil {
			t.Errorf(".claude skill %s not written: %v", sk.Name, err)
		}
		if _, err := os.Stat(skillPath(dir, filepath.Join(".devin", "skills"), sk.Name)); err == nil {
			t.Errorf(".devin skill %s written although .devin does not exist", sk.Name)
		}
	}
}

func TestWriteAllPreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte("# My project\n\nCustom rules here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteAll(dir, testServer, testVersion); err != nil {
		t.Fatalf("WriteAll failed: %v", err)
	}
	s := read(t, path)
	if !strings.Contains(s, "Custom rules here.") {
		t.Error("existing content was lost")
	}
	if !strings.Contains(s, startPrefix) {
		t.Error("block was not appended")
	}
}

func TestWriteAllIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		if _, err := WriteAll(dir, testServer, testVersion); err != nil {
			t.Fatalf("WriteAll #%d failed: %v", i+1, err)
		}
	}
	s := read(t, filepath.Join(dir, "AGENTS.md"))
	if n := strings.Count(s, startPrefix); n != 1 {
		t.Errorf("block appears %d times after re-run, want 1", n)
	}
}

func TestWriteAllReplacesLegacyBlockInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	orig := "# Notes\n\n<!-- airbuild:start -->\nold airbuild content\n" + endMarker + "\n\nMore notes.\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteAll(dir, testServer, testVersion); err != nil {
		t.Fatalf("WriteAll failed: %v", err)
	}
	s := read(t, path)
	if strings.Contains(s, "old airbuild content") {
		t.Error("stale block was not replaced")
	}
	if !strings.Contains(s, "More notes.") {
		t.Error("content after the block was lost")
	}
	if strings.Count(s, startPrefix) != 1 {
		t.Error("legacy block was duplicated instead of replaced")
	}
}

func TestSkillFileWithoutMarkerIsNeverOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := skillPath(dir, filepath.Join(".agents", "skills"), "airbuild-codepush")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "---\nname: airbuild-codepush\ndescription: my own\n---\nmine\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteAll(dir, testServer, testVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(dir, testServer, testVersion); err != nil {
		t.Fatal(err)
	}
	if read(t, path) != mine {
		t.Error("user-authored skill was overwritten")
	}
}

func TestCheckAndSyncDetectAndFixStaleFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteAll(dir, testServer, "v1.0.0"); err != nil {
		t.Fatal(err)
	}

	found, err := Check(dir, "v1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != len(Files)+len(Skills) {
		t.Fatalf("found %d managed files, want %d", len(found), len(Files)+len(Skills))
	}
	for _, f := range found {
		if !f.Stale || f.Version != "v1.0.0" {
			t.Errorf("%s: stale=%v version=%q, want stale v1.0.0", f.Path, f.Stale, f.Version)
		}
	}

	updated, err := Sync(dir, testServer, "v1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != len(Files)+len(Skills) {
		t.Errorf("Sync updated %d files, want %d", len(updated), len(Files)+len(Skills))
	}
	found, _ = Check(dir, "v1.2.0")
	for _, f := range found {
		if f.Stale {
			t.Errorf("%s still stale after Sync", f.Path)
		}
	}
	// A second sync is a no-op.
	if again, _ := Sync(dir, testServer, "v1.2.0"); len(again) != 0 {
		t.Errorf("second Sync changed %v", again)
	}
}

func TestSyncNeverCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	updated, err := Sync(dir, testServer, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 0 {
		t.Errorf("Sync created %v in an empty project", updated)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("Sync left files behind: %v", entries)
	}
}

func TestUnversionedBlockIsStaleAndDevBuildsDoNotFlagStamped(t *testing.T) {
	dir := t.TempDir()
	legacy := "<!-- airbuild:start -->\nx\n" + endMarker + "\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	found, _ := Check(dir, "dev")
	if len(found) != 1 || !found[0].Stale {
		t.Errorf("legacy block should be stale even for dev builds: %+v", found)
	}

	dir2 := t.TempDir()
	if _, err := WriteAll(dir2, testServer, "v0.1.0"); err != nil {
		t.Fatal(err)
	}
	for _, f := range mustCheck(t, dir2, "dev") {
		if f.Stale {
			t.Errorf("dev build flagged stamped file %s as stale", f.Path)
		}
	}
}

func mustCheck(t *testing.T, dir, v string) []Finding {
	t.Helper()
	f, err := Check(dir, v)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestGuideTopicsRenderForConfiguredServer(t *testing.T) {
	want := map[string][]string{
		"distribution": {"airbuild push", "airbuild upload", "airbuild links create", "airbuild builds list",
			"airbuild apps list", "airbuild config", "airbuild upgrade", ".mobileconfig", "flutter pub add airbuild",
			"dk_", "baseUrl: '" + testServer + "'", testServer + "/i/<slug>", "READY"},
		"codepush": {"codepush_flutter", "codepush_react_native", "DRAFT", "--rollout", "auto-pause", "ASK THE USER",
			"airbuild guide flutter", "airbuild guide react-native", "new binary", "Enable OTA Updates"},
		"flutter": {"codepush flutter doctor", "codepush flutter install", "codepush flutter init",
			"codepush flutter release <android|ios> --version", "codepush flutter patch <android|ios> --release-version",
			"codepush flutter promote", "codepush flutter rollback", "codepush flutter status", "--no-codesign",
			"--format apk|aab", "--dart-define", "AirBuildCodePushOverlay", "Java 17"},
		"react-native": {"codepush react-native doctor", "codepush react-native install", "codepush react-native init",
			"codepush react-native publish --platform android|ios --runtime-version", "expo.updates.runtimeVersion",
			"codepush react-native promote", "codepush react-native rollback --update-id", "codepush react-native status",
			"airbuild-telemetry", "expo-updates", "expo-channel-name"},
	}
	if len(want) != len(Topics) {
		t.Fatalf("test covers %d topics but %d exist", len(want), len(Topics))
	}
	for _, topic := range Topics {
		s, ok := Guide(topic.Name, testServer+"/")
		if !ok {
			t.Fatalf("guide %q not found", topic.Name)
		}
		for _, w := range want[topic.Name] {
			if !strings.Contains(s, w) {
				t.Errorf("guide %q missing %q", topic.Name, w)
			}
		}
		if strings.Contains(s, "__SERVER__") {
			t.Errorf("guide %q has an unreplaced placeholder", topic.Name)
		}
	}
	if _, ok := Guide("nope", testServer); ok {
		t.Error("unknown topic should not resolve")
	}
}
