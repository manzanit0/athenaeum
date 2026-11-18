package home

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestHomeRemembersLibraries checks that the list starts with the home
// folder itself and survives being read again.
func TestHomeRemembersLibraries(t *testing.T) {
	dir := t.TempDir()
	h, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h.Current() != h.Dir() || !slices.Equal(h.Libraries(), []string{h.Dir()}) {
		t.Fatalf("a new home knows %v and opens %q", h.Libraries(), h.Current())
	}
	other := filepath.Join(t.TempDir(), "novels")
	if err := h.Use(other); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Current() != other || !slices.Equal(again.Libraries(), []string{h.Dir(), other}) {
		t.Fatalf("read back %v, opening %q", again.Libraries(), again.Current())
	}
	if err := again.Forget(other); err != nil {
		t.Fatal(err)
	}
	if again.Current() != "" || !slices.Equal(again.Libraries(), []string{h.Dir()}) {
		t.Fatalf("after forgetting: %v, opening %q", again.Libraries(), again.Current())
	}
}

// TestInspect checks what each kind of folder is taken for.
func TestInspect(t *testing.T) {
	root := t.TempDir()
	write := func(parts ...string) {
		name := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("library", Catalogue)
	write("empty", ".DS_Store")
	write("home", listFile)
	write("other", "novel.epub")
	write("file")
	cases := map[string]Kind{
		"library":       KindLibrary,
		"empty":         KindEmpty,
		"home":          KindEmpty,
		"other":         KindOther,
		"new":           KindNew,
		"file":          KindInvalid,
		"missing/child": KindInvalid,
	}
	for name, want := range cases {
		if got := Inspect(filepath.Join(root, name)); got.Kind != want {
			t.Errorf("%s is taken for %q (%s), want %q", name, got.Kind, got.Detail, want)
		}
	}
	if got := Inspect("books"); got.Kind != KindInvalid {
		t.Errorf("a relative path is taken for %q", got.Kind)
	}
	if _, err := os.Stat(filepath.Join(root, "new")); err == nil {
		t.Error("inspecting created the folder")
	}
}

// TestResolve checks that a typed path is tidied and that a tilde
// stands for the user's home folder.
func TestResolve(t *testing.T) {
	user, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home folder")
	}
	cases := map[string]string{
		"  /books/novels/ ": "/books/novels",
		"~/Books":           filepath.Join(user, "Books"),
		"~":                 user,
	}
	for typed, want := range cases {
		if got, err := Resolve(typed); err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", typed, got, err, want)
		}
	}
}

// TestSuggest checks that folders, and only folders, that continue the
// typed path are suggested.
func TestSuggest(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Books", "Bookmarks", "Music", ".cache"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Book.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"boo": {"Bookmarks", "Books"},
		"":    {"Bookmarks", "Books", "Music"},
		".":   {".cache"},
		"x":   {},
	}
	for typed, names := range cases {
		want := []string{}
		for _, name := range names {
			want = append(want, filepath.Join(root, name))
		}
		if got := Suggest(root + "/" + typed); !slices.Equal(got, want) {
			t.Errorf("Suggest(%q) = %v, want %v", typed, got, want)
		}
	}
}
