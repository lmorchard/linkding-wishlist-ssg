package siteconfig

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
)

// EnvPrefix namespaces every environment variable this tool reads, so it cannot
// collide with whatever else is in a shell.
const EnvPrefix = "LINKDING_WISHLIST_"

// EnvName returns the environment variable name for an option key, e.g.
// "index-months" -> "LINKDING_WISHLIST_INDEX_MONTHS".
func EnvName(key string) string {
	upper := strings.ToUpper(key)
	upper = strings.NewReplacer("-", "_", ".", "_").Replace(upper)
	return EnvPrefix + upper
}

// Resolver layers option values in a fixed precedence:
//
//	explicit flag  >  process environment  >  .env file  >  flag default
//
// The flag default is the last resort, which is why it lives on the flag itself
// and is not passed again here.
type Resolver struct {
	// fs is consulted via Visit, which reports only flags the user actually
	// set. This is what makes "flag wins only when given" possible: a flag
	// carries its default whether or not anyone typed it, so its *value*
	// cannot distinguish the two cases.
	fs *flag.FlagSet

	// lookupEnv reads the process environment. A variable that exists but is
	// empty counts as set.
	lookupEnv func(string) (string, bool)

	// fileValues holds parsed .env contents.
	fileValues map[string]string

	// warns receives a message when a value is present but unparseable. A bad
	// value is skipped rather than fatal, so a stale .env cannot break a build
	// that overrides it on the command line.
	warns func(string)
}

// NewResolver builds a Resolver. lookupEnv and warns may be nil.
func NewResolver(fs *flag.FlagSet, lookupEnv func(string) (string, bool), fileValues map[string]string, warns func(string)) *Resolver {
	if lookupEnv == nil {
		lookupEnv = func(string) (string, bool) { return "", false }
	}
	if warns == nil {
		warns = func(string) {}
	}
	if fileValues == nil {
		fileValues = map[string]string{}
	}
	return &Resolver{fs: fs, lookupEnv: lookupEnv, fileValues: fileValues, warns: warns}
}

// source reports where a key's value came from, for diagnostics.
type source int

const (
	sourceDefault source = iota
	sourceFile
	sourceEnv
	sourceFlag
)

func (s source) String() string {
	switch s {
	case sourceFlag:
		return "flag"
	case sourceEnv:
		return "env"
	case sourceFile:
		return ".env"
	default:
		return "default"
	}
}

// lookup returns the raw string value for key and where it came from.
// The flag layer is handled separately by the typed accessors, which read the
// already-parsed flag value.
func (r *Resolver) lookup(key string) (value string, from source, found bool) {
	envKey := EnvName(key)

	// Process environment beats .env. An empty-but-set variable is meaningful:
	// it lets the user clear a .env value without editing the file.
	if v, ok := r.lookupEnv(envKey); ok {
		return v, sourceEnv, true
	}
	if v, ok := r.fileValues[envKey]; ok {
		return v, sourceFile, true
	}
	return "", sourceDefault, false
}

// flagSet reports whether the user explicitly provided key on the command line.
func (r *Resolver) flagSet(key string) bool {
	set := false
	r.fs.Visit(func(f *flag.Flag) {
		if f.Name == key {
			set = true
		}
	})
	return set
}

// String returns the winning value for a string option.
func (r *Resolver) String(key string) string {
	if r.flagSet(key) {
		return r.fs.Lookup(key).Value.String()
	}
	if v, _, found := r.lookup(key); found {
		return v
	}
	return r.fs.Lookup(key).Value.String() // the flag default
}

// Int returns the winning value for an int option.
func (r *Resolver) Int(key string) int {
	if r.flagSet(key) {
		return intValue(r.fs, key)
	}
	if raw, from, found := r.lookup(key); found {
		parsed, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			r.warns(fmt.Sprintf("ignoring %s from %s: %v", EnvName(key), from, err))
		} else {
			return parsed
		}
	}
	return intValue(r.fs, key)
}

// Bool returns the winning value for a bool option.
func (r *Resolver) Bool(key string) bool {
	if r.flagSet(key) {
		return boolValue(r.fs, key)
	}
	if raw, from, found := r.lookup(key); found {
		parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			r.warns(fmt.Sprintf("ignoring %s from %s: %v", EnvName(key), from, err))
		} else {
			return parsed
		}
	}
	return boolValue(r.fs, key)
}

func intValue(fs *flag.FlagSet, key string) int {
	f := fs.Lookup(key)
	if f == nil {
		return 0
	}
	if g, ok := f.Value.(flag.Getter); ok {
		if v, ok := g.Get().(int); ok {
			return v
		}
	}
	parsed, _ := strconv.Atoi(f.Value.String())
	return parsed
}

func boolValue(fs *flag.FlagSet, key string) bool {
	f := fs.Lookup(key)
	if f == nil {
		return false
	}
	if g, ok := f.Value.(flag.Getter); ok {
		if v, ok := g.Get().(bool); ok {
			return v
		}
	}
	parsed, _ := strconv.ParseBool(f.Value.String())
	return parsed
}
