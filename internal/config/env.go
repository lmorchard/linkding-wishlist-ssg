// Package config loads optional .env credential files.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadEnv reads KEY=VALUE pairs from path. A missing file yields an empty map
// and no error. The process environment is never modified; callers decide how
// to merge the values.
//
// Supported syntax is deliberately small: blank lines, # comments, optional
// "export " prefixes, and single- or double-quoted values.
func LoadEnv(path string) (map[string]string, error) {
	values := make(map[string]string)

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return values, fmt.Errorf("opening %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := parseLine(scanner.Text())
		if !ok {
			continue
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return values, fmt.Errorf("reading %s: %w", path, err)
	}

	return values, nil
}

func parseLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}

	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}

	key, value, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}

	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			value = value[1 : len(value)-1]
		}
	}

	return key, value, true
}
