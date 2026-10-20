// Package config loads local startup settings without logging their values.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LoadEnv reads KEY=value lines, ignoring blanks and full-line # comments.
// Single quotes are literal; double quotes support Go-style escapes.
// There is no variable expansion. Existing process variables take precedence.
// A missing file is allowed so environment-only deployments still work.
func LoadEnv(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("could not open local .env file")
	}
	defer f.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || !envName.MatchString(key) {
			return fmt.Errorf("invalid .env syntax on line %d", line)
		}
		if strings.HasPrefix(value, `"`) {
			decoded, err := strconv.Unquote(value)
			if err != nil {
				return fmt.Errorf("invalid .env quoting on line %d", line)
			}
			value = decoded
		} else if strings.HasPrefix(value, "'") {
			if len(value) < 2 || !strings.HasSuffix(value, "'") {
				return fmt.Errorf("invalid .env quoting on line %d", line)
			}
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if scanner.Err() != nil {
		return errors.New("could not read local .env file")
	}
	for key, value := range values {
		if _, exists := os.LookupEnv(key); !exists {
			if os.Setenv(key, value) != nil {
				return errors.New("could not apply local .env settings")
			}
		}
	}
	return nil
}
