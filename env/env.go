package env

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Load reads a .env file into the process environment; existing variables
// are not overridden.
func Load(filenames ...string) error {
	return godotenvLoad(filenames...)
}

// Get returns the string value of key, or the first default if unset. A few
// literal values are cast rather than passed through raw: see castString.
func Get(key string, defaultValue ...string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return first(defaultValue)
	}
	return castString(value)
}

// Has reports whether key is set in the environment (even to an empty string).
func Has(key string) bool {
	_, ok := os.LookupEnv(key)
	return ok
}

// MustGet returns the value of key, panicking if it is not set.
func MustGet(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		panic(fmt.Sprintf("env: required environment variable %q is not set", key))
	}
	return castString(value)
}

// GetString is an alias of Get, for naming symmetry with GetInt/GetBool/GetFloat.
func GetString(key string, defaultValue ...string) string {
	return Get(key, defaultValue...)
}

// GetInt returns key parsed as an int, or the first default if unset/invalid.
func GetInt(key string, defaultValue ...int) int {
	value, ok := os.LookupEnv(key)
	if !ok {
		return first(defaultValue)
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return first(defaultValue)
	}
	return parsed
}

// GetFloat returns key parsed as a float64, or the first default if unset/invalid.
func GetFloat(key string, defaultValue ...float64) float64 {
	value, ok := os.LookupEnv(key)
	if !ok {
		return first(defaultValue)
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return first(defaultValue)
	}
	return parsed
}

// GetBool returns key parsed as a bool, or the first default if unset/invalid.
// Recognizes "true"/"false", "1"/"0", "yes"/"no", "on"/"off" (case-insensitive).
func GetBool(key string, defaultValue ...bool) bool {
	value, ok := os.LookupEnv(key)
	if !ok {
		return first(defaultValue)
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		// Includes "": explicitly set to blank is not a recognized token, so
		// it falls back to the default like any other unrecognized value,
		// same as an unset key — it must not silently mean false.
		return first(defaultValue)
	}
}

// GetSlice splits key on sep (default ",") and trims whitespace from each
// element, dropping empty elements. Useful for CSV-style env values.
func GetSlice(key string, sep string, defaultValue ...[]string) []string {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return first(defaultValue)
	}
	if sep == "" {
		sep = ","
	}
	parts := strings.Split(value, sep)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// Environment returns APP_ENV, defaulting to "production".
func Environment() string {
	return Get("APP_ENV", "production")
}

// Is reports whether Environment() matches any of the given names
// (case-insensitive), e.g. env.Is("local", "testing").
func Is(names ...string) bool {
	current := strings.ToLower(Environment())
	for _, name := range names {
		if strings.ToLower(name) == current {
			return true
		}
	}
	return false
}

func IsProduction() bool { return Is("production") }

func IsLocal() bool { return Is("local") }

func IsTesting() bool { return Is("testing") }

// castString applies env()'s special-case string casts: "true"/"false" pass
// through, "null"/"empty" become "".
func castString(value string) string {
	switch strings.ToLower(value) {
	case "true":
		return "true"
	case "false":
		return "false"
	case "null":
		return ""
	case "empty":
		return ""
	default:
		return value
	}
}

// first returns the first element of values, or T's zero value if empty.
// Backs the variadic default-value parameter shared by every Get* function.
func first[T any](values []T) T {
	var zero T
	if len(values) > 0 {
		return values[0]
	}
	return zero
}
