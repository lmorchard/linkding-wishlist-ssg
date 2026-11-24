package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadEnvMissingFileIsNotAnError(t *testing.T) {
	values, err := LoadEnv(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatalf("LoadEnv returned error for missing file: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("values = %#v, want empty", values)
	}
}

func TestLoadEnvParsesCredentialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "# Linkding credentials\n" +
		"\n" +
		"LINKDING_URL=https://links.example.com\n" +
		"export LINKDING_TOKEN=plain-token\n" +
		"QUOTED_DOUBLE=\"value with spaces\"\n" +
		"QUOTED_SINGLE='single value'\n" +
		"EQUALS=left=right\n" +
		"EMPTY=\n" +
		"  SPACED  =  trimmed  \n" +
		"malformed line without equals\n" +
		"=missing-key\n" +
		"CRLF=windows\r\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	values, err := LoadEnv(path)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"LINKDING_URL":   "https://links.example.com",
		"LINKDING_TOKEN": "plain-token",
		"QUOTED_DOUBLE":  "value with spaces",
		"QUOTED_SINGLE":  "single value",
		"EQUALS":         "left=right",
		"EMPTY":          "",
		"SPACED":         "trimmed",
		"CRLF":           "windows",
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("values = %#v, want %#v", values, want)
	}
}

func TestLoadEnvReportsReadError(t *testing.T) {
	if _, err := LoadEnv(t.TempDir()); err == nil {
		t.Fatal("expected an error when the path is a directory")
	}
}

func TestLoadEnvDoesNotModifyProcessEnvironment(t *testing.T) {
	const key = "LINKDING_WISHLIST_SSG_TEST_UNSET"
	if _, exists := os.LookupEnv(key); exists {
		t.Skipf("%s is already set", key)
	}

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(key+"=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEnv(path); err != nil {
		t.Fatal(err)
	}
	if _, exists := os.LookupEnv(key); exists {
		t.Fatalf("LoadEnv modified the process environment")
	}
}
