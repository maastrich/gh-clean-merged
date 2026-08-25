package git

import "testing"

func TestSquashMerged(t *testing.T) {
	fixture(t)

	cases := []struct {
		branch string
		want   SquashState
	}{
		{"squashed", SquashApplied},
		{"unmerged", SquashUnknown},
		{"empty", SquashEmpty},
		{"noop", SquashEmpty},
	}

	for _, tc := range cases {
		t.Run(tc.branch, func(t *testing.T) {
			got, err := SquashMerged(tc.branch, "origin/main")
			if err != nil {
				t.Fatalf("SquashMerged(%q): %v", tc.branch, err)
			}
			if got != tc.want {
				t.Errorf("SquashMerged(%q) = %v, want %v", tc.branch, got, tc.want)
			}
		})
	}
}

func TestIsAncestor(t *testing.T) {
	fixture(t)

	if !IsAncestor("merged", "origin/main") {
		t.Error("merged should be an ancestor of origin/main")
	}
	// A squash merge leaves no ancestry link, which is exactly why the patch-id
	// comparison exists.
	if IsAncestor("squashed", "origin/main") {
		t.Error("squashed should not be an ancestor of origin/main")
	}
	if IsAncestor("unmerged", "origin/main") {
		t.Error("unmerged should not be an ancestor of origin/main")
	}
}

func TestDefaultBranch(t *testing.T) {
	fixture(t)

	got, err := DefaultBranch("origin")
	if err != nil {
		t.Fatal(err)
	}
	if got != "main" {
		t.Errorf("DefaultBranch = %q, want %q", got, "main")
	}
}

func TestLocalBranches(t *testing.T) {
	fixture(t)

	branches, err := LocalBranches()
	if err != nil {
		t.Fatal(err)
	}

	names := map[string]bool{}
	for _, b := range branches {
		names[b.Name] = true
	}
	for _, want := range []string{"main", "merged", "squashed", "unmerged", "empty", "noop"} {
		if !names[want] {
			t.Errorf("LocalBranches is missing %q", want)
		}
	}
}

func TestHasRemoteRef(t *testing.T) {
	fixture(t)

	if !HasRemoteRef("origin", "main") {
		t.Error("origin/main should exist")
	}
	if HasRemoteRef("origin", "nope") {
		t.Error("origin/nope should not exist")
	}
}
