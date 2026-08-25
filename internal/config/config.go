// Package config reads the optional configuration files.
//
// Two files may exist, and both are optional: a global one for the way you like
// to work everywhere, and a local one at the root of a repository for what that
// repository needs. They are merged, then the command line flags are applied on
// top, so the order of precedence reads defaults, global, local, flags.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Where the files live: the global one under the user's config home, the local
// one at the root of a repository.
const (
	GlobalDir = "gh-clean-merged"
	FileName  = "config.json"
	LocalName = ".gh-clean-merged.json"
)

// SchemaURL describes the file format for editors. Files written by
// `gh clean-merged config set` point at it, so completion and validation work
// out of the box in anything that speaks JSON schema.
const SchemaURL = "https://raw.githubusercontent.com/maastrich/gh-clean-merged/main/schema.json"

// File mirrors the JSON on disk. Every field is a pointer so an absent key can
// be told apart from one explicitly set to the zero value: a local file must be
// able to turn off what the global file turned on.
type File struct {
	// Schema is the editor hint, kept so rewriting a file does not drop it.
	Schema     *string  `json:"$schema,omitempty"`
	Base       *string  `json:"base,omitempty"`
	Remote     *string  `json:"remote,omitempty"`
	Protected  []string `json:"protected,omitempty"`
	KeepClosed *bool    `json:"keepClosed,omitempty"`
	NoFetch    *bool    `json:"noFetch,omitempty"`
	Verbose    *bool    `json:"verbose,omitempty"`
	DryRun     *bool    `json:"dryRun,omitempty"`
	Color      *string  `json:"color,omitempty"`
}

// Config is what the files add up to, plus where it came from.
type Config struct {
	File
	// Sources lists the files that contributed, nearest last.
	Sources []string
}

// LocalPath is where a repository's own file lives.
func LocalPath(repoRoot string) string {
	return filepath.Join(repoRoot, LocalName)
}

// Read loads a single file. A missing file reads as an empty one, so callers
// editing a file need not care whether it exists yet.
func Read(path string) (File, error) {
	file, _, err := read(path)
	return file, err
}

// Save writes a file, creating the directory if needed, and points it at the
// schema so editors can help.
func Save(path string, file File) error {
	if file.Schema == nil {
		schema := SchemaURL
		file.Schema = &schema
	}

	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode %s: %w", path, err)
	}
	raw = append(raw, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// Load reads the global file and, when repoRoot is not empty, the local one,
// and merges them. Missing files are not an error; malformed ones are.
func Load(repoRoot string) (Config, error) {
	var cfg Config

	paths := []string{GlobalPath()}
	if repoRoot != "" {
		paths = append(paths, filepath.Join(repoRoot, LocalName))
	}

	for _, path := range paths {
		if path == "" {
			continue
		}
		file, found, err := read(path)
		if err != nil {
			return Config{}, err
		}
		if !found {
			continue
		}
		cfg.File = merge(cfg.File, file)
		cfg.Sources = append(cfg.Sources, path)
	}
	return cfg, nil
}

// GlobalPath is where the global file lives, following XDG_CONFIG_HOME when it
// is set. It is empty when neither that nor a home directory is known.
func GlobalPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, GlobalDir, FileName)
}

func read(path string) (File, bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, fmt.Errorf("failed to read %s: %w", path, err)
	}

	var file File
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// A misspelt key would otherwise be silently ignored, and the user would be
	// left wondering why their setting does nothing.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return File{}, false, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return file, true, nil
}

// merge lays next over base. Scalars are replaced when set, while protected
// branches accumulate: a repository adds its deploy branches to whatever the
// global file already protects, rather than having to repeat them.
func merge(base, next File) File {
	if next.Base != nil {
		base.Base = next.Base
	}
	if next.Remote != nil {
		base.Remote = next.Remote
	}
	if next.KeepClosed != nil {
		base.KeepClosed = next.KeepClosed
	}
	if next.NoFetch != nil {
		base.NoFetch = next.NoFetch
	}
	if next.Verbose != nil {
		base.Verbose = next.Verbose
	}
	if next.DryRun != nil {
		base.DryRun = next.DryRun
	}
	if next.Color != nil {
		base.Color = next.Color
	}
	if next.Schema != nil {
		base.Schema = next.Schema
	}
	base.Protected = union(base.Protected, next.Protected)
	return base
}

// union appends what is missing, keeping the order the patterns were written in.
func union(base, next []string) []string {
	seen := make(map[string]bool, len(base))
	for _, item := range base {
		seen[item] = true
	}
	for _, item := range next {
		if !seen[item] {
			base = append(base, item)
			seen[item] = true
		}
	}
	return base
}
