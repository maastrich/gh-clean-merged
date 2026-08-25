package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSetAndUnsetScalars(t *testing.T) {
	var file File

	if err := Set(&file, "remote", []string{"upstream"}); err != nil {
		t.Fatal(err)
	}
	if file.Remote == nil || *file.Remote != "upstream" {
		t.Fatalf("remote = %v, want upstream", file.Remote)
	}

	if err := Set(&file, "keepClosed", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	if file.KeepClosed == nil || !*file.KeepClosed {
		t.Fatalf("keepClosed = %v, want true", file.KeepClosed)
	}

	// Unsetting hands the decision back to the next file up the chain, which is
	// different from writing false.
	if err := Unset(&file, "keepClosed"); err != nil {
		t.Fatal(err)
	}
	if file.KeepClosed != nil {
		t.Errorf("keepClosed = %v, want absent", file.KeepClosed)
	}
}

func TestSetRejectsBadValues(t *testing.T) {
	var file File

	if err := Set(&file, "keepClosed", []string{"maybe"}); err == nil {
		t.Error("expected an error for a value that is not a boolean")
	}
	if err := Set(&file, "remote", []string{"a", "b"}); err == nil {
		t.Error("expected an error for two values on a single valued key")
	}
	if err := Set(&file, "nope", []string{"x"}); err == nil {
		t.Error("expected an error for an unknown key")
	}
}

func TestListKeyAddAndRemove(t *testing.T) {
	var file File

	if err := Add(&file, "protected", []string{"prod/*", "release"}); err != nil {
		t.Fatal(err)
	}
	// Adding what is already there changes nothing.
	if err := Add(&file, "protected", []string{"release"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"prod/*", "release"}; !reflect.DeepEqual(file.Protected, want) {
		t.Fatalf("protected = %v, want %v", file.Protected, want)
	}

	if err := Remove(&file, "protected", []string{"prod/*"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"release"}; !reflect.DeepEqual(file.Protected, want) {
		t.Fatalf("protected = %v, want %v", file.Protected, want)
	}

	// Set replaces rather than appends.
	if err := Set(&file, "protected", []string{"beta/*"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"beta/*"}; !reflect.DeepEqual(file.Protected, want) {
		t.Errorf("protected = %v, want %v", file.Protected, want)
	}
}

func TestAddRejectsScalarKeys(t *testing.T) {
	var file File
	if err := Add(&file, "remote", []string{"upstream"}); err == nil {
		t.Error("expected an error when appending to a scalar key")
	}
}

// A file written by the command line must read back the same, and carry the
// schema so editors can help.
func TestSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), LocalName)

	var file File
	if err := Set(&file, "base", []string{"main"}); err != nil {
		t.Fatal(err)
	}
	if err := Add(&file, "protected", []string{"prod/*"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, file); err != nil {
		t.Fatal(err)
	}

	back, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Base == nil || *back.Base != "main" {
		t.Errorf("base = %v, want main", back.Base)
	}
	if back.Schema == nil || *back.Schema != SchemaURL {
		t.Errorf("schema = %v, want %s", back.Schema, SchemaURL)
	}
}

// The published schema is what editors validate against, so it must describe
// exactly the keys the tool accepts.
func TestSchemaMatchesTheKeys(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}

	var schema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties {
		t.Error("the schema should reject unknown keys, as the loader does")
	}

	described := map[string]bool{}
	for name := range schema.Properties {
		if name != "$schema" {
			described[name] = true
		}
	}

	for _, name := range Keys() {
		if !described[name] {
			t.Errorf("key %q is missing from schema.json", name)
		}
		delete(described, name)
	}
	for name := range described {
		t.Errorf("schema.json describes %q, which the tool does not accept", name)
	}
}
