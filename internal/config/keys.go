package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// kind tells the command line how to parse a value and how to print it back.
type kind int

const (
	kindString kind = iota
	kindBool
	kindList
)

type key struct {
	kind kind
	// get and set reach the matching field of a File.
	get func(File) (string, bool)
	set func(*File, []string) error
	// unset returns the field to its absent state.
	unset func(*File)
}

// keys is the whole configurable surface, named as the JSON keys are.
var keys = map[string]key{
	"base":       stringKey(func(f *File) **string { return &f.Base }),
	"remote":     stringKey(func(f *File) **string { return &f.Remote }),
	"color":      stringKey(func(f *File) **string { return &f.Color }),
	"keepClosed": boolKey(func(f *File) **bool { return &f.KeepClosed }),
	"noFetch":    boolKey(func(f *File) **bool { return &f.NoFetch }),
	"verbose":    boolKey(func(f *File) **bool { return &f.Verbose }),
	"dryRun":     boolKey(func(f *File) **bool { return &f.DryRun }),
	"protected": {
		kind: kindList,
		get: func(f File) (string, bool) {
			if len(f.Protected) == 0 {
				return "", false
			}
			return strings.Join(f.Protected, ", "), true
		},
		set: func(f *File, values []string) error {
			f.Protected = append([]string(nil), values...)
			return nil
		},
		unset: func(f *File) { f.Protected = nil },
	},
}

func stringKey(field func(*File) **string) key {
	return key{
		kind: kindString,
		get: func(f File) (string, bool) {
			value := *field(&f)
			if value == nil {
				return "", false
			}
			return *value, true
		},
		set: func(f *File, values []string) error {
			if len(values) != 1 {
				return fmt.Errorf("expected a single value, got %d", len(values))
			}
			value := values[0]
			*field(f) = &value
			return nil
		},
		unset: func(f *File) { *field(f) = nil },
	}
}

func boolKey(field func(*File) **bool) key {
	return key{
		kind: kindBool,
		get: func(f File) (string, bool) {
			value := *field(&f)
			if value == nil {
				return "", false
			}
			return strconv.FormatBool(*value), true
		},
		set: func(f *File, values []string) error {
			if len(values) != 1 {
				return fmt.Errorf("expected a single value, got %d", len(values))
			}
			value, err := strconv.ParseBool(values[0])
			if err != nil {
				return fmt.Errorf("expected true or false, got %q", values[0])
			}
			*field(f) = &value
			return nil
		},
		unset: func(f *File) { *field(f) = nil },
	}
}

// Keys lists every configurable key, sorted, for help and error messages.
func Keys() []string {
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func lookup(name string) (key, error) {
	k, ok := keys[name]
	if !ok {
		return key{}, fmt.Errorf("unknown key %q, expected one of %s", name, strings.Join(Keys(), ", "))
	}
	return k, nil
}

// Get reads one key, and reports whether it was set at all.
func Get(file File, name string) (string, bool, error) {
	k, err := lookup(name)
	if err != nil {
		return "", false, err
	}
	value, ok := k.get(file)
	return value, ok, nil
}

// Set replaces one key with the given values.
func Set(file *File, name string, values []string) error {
	k, err := lookup(name)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return fmt.Errorf("%s needs a value", name)
	}
	if err := k.set(file, values); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Unset returns one key to its absent state, so the next file up the chain
// decides again.
func Unset(file *File, name string) error {
	k, err := lookup(name)
	if err != nil {
		return err
	}
	k.unset(file)
	return nil
}

// Add appends values to a list key, skipping what is already there.
func Add(file *File, name string, values []string) error {
	k, err := lookup(name)
	if err != nil {
		return err
	}
	if k.kind != kindList {
		return fmt.Errorf("%s is not a list, use `config set %s`", name, name)
	}
	file.Protected = union(file.Protected, values)
	return nil
}

// Remove drops values from a list key.
func Remove(file *File, name string, values []string) error {
	k, err := lookup(name)
	if err != nil {
		return err
	}
	if k.kind != kindList {
		return fmt.Errorf("%s is not a list, use `config unset %s`", name, name)
	}

	drop := make(map[string]bool, len(values))
	for _, value := range values {
		drop[value] = true
	}
	kept := make([]string, 0, len(file.Protected))
	for _, item := range file.Protected {
		if !drop[item] {
			kept = append(kept, item)
		}
	}
	if len(kept) == 0 {
		kept = nil
	}
	file.Protected = kept
	return nil
}
