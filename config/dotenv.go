package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// envFile is the file Load reads before it reads the environment.
//
// One name, and it is not configurable. There is no --env-file flag and no
// variable that moves it: a second place to put configuration is a second place
// to look for it when the wrong value is in effect.
const envFile = ".env"

// loadEnvFile fills the gaps in the process environment from ./.env.
//
// # Why it exists
//
// `aru new crm` writes a .env with a fresh APP_KEY and then prints "cd crm &&
// aru migrate". Nothing read that file, so the first command of every new
// project died with "APP_KEY must be 32 bytes, got 0 (run `aru key:generate`)"
// -- telling the reader to generate the key that was already on disk.
//
// # Precedence, which is the part that matters
//
// A variable that is already defined in the environment always wins; the file
// only fills what is missing. That is what keeps production intact: the
// orchestrator sets the real values, and a .env that got copied into an image
// by accident cannot override them. Never invert this.
//
// "Already defined" means present, not non-empty. A deployment that exports a
// variable as the empty string has made a decision, and a file on disk does not
// get to overrule it.
//
// # The format is closed
//
// KEY=value, blank lines, whole-line comments starting with #, an optional
// `export ` prefix, and optional single or double quotes wrapping the value.
// That is all of it. No ${INTERPOLATION}, no multi-line values, no include, no
// inline comment after a value -- a # in a password is a # in a password. A
// line the format does not cover is an error naming the file and the line
// rather than a variable that silently did not arrive.
//
// A missing file is not an error. That is production: the environment is
// already populated and there is no file at all.
func loadEnvFile() error {
	body, err := os.ReadFile(envFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", envFile, err)
	}

	for i, raw := range strings.Split(string(body), "\n") {
		number := i + 1

		// CRLF costs nothing to accept here and is otherwise a trailing \r
		// inside a value, which surfaces as a password that does not match.
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		name, value, ok := strings.Cut(line, "=")
		if !ok {
			// The line is not echoed. A line with no "=" can be anything,
			// including a secret somebody pasted, and this error reaches logs.
			return fmt.Errorf("%s:%d: expected KEY=value", envFile, number)
		}
		name = strings.TrimSpace(name)
		if !validName(name) {
			return fmt.Errorf("%s:%d: %q is not a variable name: expected a letter or underscore, then letters, digits or underscores",
				envFile, number, name)
		}

		value, err := unquote(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("%s:%d: the value of %s %s", envFile, number, name, err)
		}

		if _, defined := os.LookupEnv(name); defined {
			continue
		}
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("%s:%d: setting %s: %w", envFile, number, name, err)
		}
	}
	return nil
}

// validName reports whether the key is a name a shell would accept. Anything
// else is a typo -- most often a missing "=" on the line above, which turns the
// rest of the file into one long name.
func validName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// unquote strips one layer of matching single or double quotes.
//
// The quotes are optional and carry no meaning beyond "this is where the value
// ends": nothing is unescaped inside them, because escaping is the first step
// down the road that ends in a shell parser.
//
// A value that opens with a quote and does not close is an error. The
// alternative is a value that keeps its quote character and fails much later,
// as a token that does not verify or a path that does not exist.
func unquote(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	q := value[0]
	if q != '"' && q != '\'' {
		return value, nil
	}
	if len(value) < 2 || value[len(value)-1] != q {
		return "", fmt.Errorf("opens with %c and never closes: quotes wrap the whole value or are left out", q)
	}
	return value[1 : len(value)-1], nil
}
