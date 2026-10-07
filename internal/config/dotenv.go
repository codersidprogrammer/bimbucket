package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadDotEnvAt reads a dotenv file and sets any keys that are not already
// present in the environment, so real environment variables always win. A
// missing file is only an error when optional is false.
func loadDotEnvAt(path string, optional bool) error {
	f, err := os.Open(path)
	if err != nil {
		if optional && os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s from %s: %w", key, path, err)
		}
	}
	return sc.Err()
}
