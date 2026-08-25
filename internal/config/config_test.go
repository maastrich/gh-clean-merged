package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func str(s string) *string { return &s }
func boolean(b bool) *bool { return &b }

func TestLoadWithoutFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 0 || cfg.Base != nil || cfg.Protected != nil {
		t.Errorf("missing files should leave an empty config, got %+v", cfg)
	}
}

// The local file speaks for the repository, so it overrides the global one,
// except for protected patterns which add up.
func TestLoadMergesGlobalAndLocal(t *testing.T) {
	configHome, repo := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	write(t, filepath.Join(configHome, GlobalDir, FileName), `{
		"remote": "upstream",
		"protected": ["release"],
		"keepClosed": true,
		"verbose": true
	}`)
	write(t, filepath.Join(repo, LocalName), `{
		"base": "master",
		"protected": ["protected/*", "release"],
		"keepClosed": false
	}`)

	cfg, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Base == nil || *cfg.Base != "master" {
		t.Errorf("base = %v, want master from the local file", cfg.Base)
	}
	if cfg.Remote == nil || *cfg.Remote != "upstream" {
		t.Errorf("remote = %v, want upstream from the global file", cfg.Remote)
	}
	// An explicit false in the local file must undo the global true, which is
	// why the fields are pointers.
	if cfg.KeepClosed == nil || *cfg.KeepClosed {
		t.Errorf("keepClosed = %v, want the local false to win", cfg.KeepClosed)
	}
	if cfg.Verbose == nil || !*cfg.Verbose {
		t.Errorf("verbose = %v, want the global true to survive", cfg.Verbose)
	}
	// Patterns accumulate, in order, without repeating what both files list.
	if want := []string{"release", "protected/*"}; !reflect.DeepEqual(cfg.Protected, want) {
		t.Errorf("protected = %v, want %v", cfg.Protected, want)
	}
	if len(cfg.Sources) != 2 {
		t.Errorf("sources = %v, want both files", cfg.Sources)
	}
}

func TestLoadWithoutRepositoryReadsGlobalOnly(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	write(t, filepath.Join(configHome, GlobalDir, FileName), `{"remote": "upstream"}`)

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Remote == nil || *cfg.Remote != "upstream" {
		t.Errorf("remote = %v, want upstream", cfg.Remote)
	}
}

// A misspelt key silently doing nothing is worse than a clear failure.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	write(t, filepath.Join(repo, LocalName), `{"protcted": ["protected/*"]}`)

	_, err := Load(repo)
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
	if !strings.Contains(err.Error(), LocalName) {
		t.Errorf("error should name the file: %v", err)
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	write(t, filepath.Join(repo, LocalName), `{`)

	if _, err := Load(repo); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestGlobalPathFollowsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/somewhere")
	if got, want := GlobalPath(), filepath.Join("/somewhere", GlobalDir, FileName); got != want {
		t.Errorf("GlobalPath = %q, want %q", got, want)
	}
}

func TestMergeKeepsBaseWhenNextIsSilent(t *testing.T) {
	base := File{Remote: str("origin"), Verbose: boolean(true)}
	got := merge(base, File{})

	if got.Remote == nil || *got.Remote != "origin" || got.Verbose == nil || !*got.Verbose {
		t.Errorf("merge dropped values the next file said nothing about: %+v", got)
	}
}
