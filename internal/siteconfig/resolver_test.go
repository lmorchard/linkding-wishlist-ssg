package siteconfig

import (
	"flag"
	"strings"
	"testing"
)

func newFlagSet() (*flag.FlagSet, *string, *int, *bool) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	s := fs.String("pinned-tag", "wishlist:featured", "")
	n := fs.Int("index-months", 12, "")
	b := fs.Bool("no-lint", false, "")
	return fs, s, n, b
}

func envFunc(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
}

func TestEnvName(t *testing.T) {
	cases := map[string]string{
		"index-months": "LINKDING_WISHLIST_INDEX_MONTHS",
		"pinned-tag":   "LINKDING_WISHLIST_PINNED_TAG",
		"base-url":     "LINKDING_WISHLIST_BASE_URL",
		"exclude-tags": "LINKDING_WISHLIST_EXCLUDE_TAGS",
		"no-lint":      "LINKDING_WISHLIST_NO_LINT",
	}
	for key, want := range cases {
		if got := EnvName(key); got != want {
			t.Errorf("EnvName(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestResolverDefaultWhenNothingSet(t *testing.T) {
	fs, _, _, _ := newFlagSet()
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(fs, envFunc(nil), nil, nil)

	if got := r.String("pinned-tag"); got != "wishlist:featured" {
		t.Errorf("string = %q, want the flag default", got)
	}
	if got := r.Int("index-months"); got != 12 {
		t.Errorf("int = %d, want 12", got)
	}
	if got := r.Bool("no-lint"); got != false {
		t.Errorf("bool = %v, want false", got)
	}
}

func TestResolverEnvBeatsDefault(t *testing.T) {
	fs, _, _, _ := newFlagSet()
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(fs,
		envFunc(map[string]string{"LINKDING_WISHLIST_INDEX_MONTHS": "3"}),
		nil, nil)

	if got := r.Int("index-months"); got != 3 {
		t.Errorf("int = %d, want 3 from env", got)
	}
}

func TestResolverFlagBeatsEnv(t *testing.T) {
	fs, _, _, _ := newFlagSet()
	if err := fs.Parse([]string{"-index-months", "6"}); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(fs,
		envFunc(map[string]string{"LINKDING_WISHLIST_INDEX_MONTHS": "3"}),
		nil, nil)

	if got := r.Int("index-months"); got != 6 {
		t.Errorf("int = %d, want 6 from the flag", got)
	}
}

func TestResolverEnvBeatsFile(t *testing.T) {
	fs, _, _, _ := newFlagSet()
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(fs,
		envFunc(map[string]string{"LINKDING_WISHLIST_INDEX_MONTHS": "3"}),
		map[string]string{"LINKDING_WISHLIST_INDEX_MONTHS": "99"}, nil)

	if got := r.Int("index-months"); got != 3 {
		t.Errorf("int = %d, want 3 from the environment", got)
	}
}

func TestResolverFileBeatsDefault(t *testing.T) {
	fs, _, _, _ := newFlagSet()
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(fs, envFunc(nil),
		map[string]string{"LINKDING_WISHLIST_PINNED_TAG": "starred"}, nil)

	if got := r.String("pinned-tag"); got != "starred" {
		t.Errorf("string = %q, want starred from .env", got)
	}
}

func TestResolverFlagBeatsFile(t *testing.T) {
	fs, _, _, _ := newFlagSet()
	if err := fs.Parse([]string{"-pinned-tag", "featured"}); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(fs, envFunc(nil),
		map[string]string{"LINKDING_WISHLIST_PINNED_TAG": "starred"}, nil)

	if got := r.String("pinned-tag"); got != "featured" {
		t.Errorf("string = %q, want featured from the flag", got)
	}
}

func TestResolverEmptyButSetEnvOverridesFile(t *testing.T) {
	fs, _, _, _ := newFlagSet()
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	// A variable set to the empty string is still a deliberate value: it lets
	// the user clear a .env setting without editing the file.
	r := NewResolver(fs,
		envFunc(map[string]string{"LINKDING_WISHLIST_PINNED_TAG": ""}),
		map[string]string{"LINKDING_WISHLIST_PINNED_TAG": "starred"}, nil)

	if got := r.String("pinned-tag"); got != "" {
		t.Errorf("string = %q, want empty from the environment", got)
	}
}

func TestResolverBadValueWarnsAndFallsBack(t *testing.T) {
	fs, _, _, _ := newFlagSet()
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}

	var warned []string
	r := NewResolver(fs,
		envFunc(map[string]string{"LINKDING_WISHLIST_INDEX_MONTHS": "banana"}),
		nil,
		func(msg string) { warned = append(warned, msg) })

	if got := r.Int("index-months"); got != 12 {
		t.Errorf("int = %d, want the default 12 after a bad env value", got)
	}
	if len(warned) != 1 {
		t.Fatalf("warnings = %v, want one", warned)
	}
	if !strings.Contains(warned[0], "LINKDING_WISHLIST_INDEX_MONTHS") {
		t.Errorf("warning %q should name the variable", warned[0])
	}
}

func TestResolverBoolFromEnv(t *testing.T) {
	fs, _, _, _ := newFlagSet()
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(fs,
		envFunc(map[string]string{"LINKDING_WISHLIST_NO_LINT": "true"}),
		nil, nil)

	if got := r.Bool("no-lint"); got != true {
		t.Errorf("bool = %v, want true from env", got)
	}
}

func TestResolverBareFlagBeatsEnvFalse(t *testing.T) {
	fs, _, _, _ := newFlagSet()
	// The classic trap: a bool flag with default false, given bare as
	// "-no-lint", must win over an env var setting it false.
	if err := fs.Parse([]string{"-no-lint"}); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(fs,
		envFunc(map[string]string{"LINKDING_WISHLIST_NO_LINT": "false"}),
		nil, nil)

	if got := r.Bool("no-lint"); got != true {
		t.Errorf("bool = %v, want true from the bare flag", got)
	}
}
