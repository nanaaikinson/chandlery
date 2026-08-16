package env

import "testing"

func TestGet(t *testing.T) {
	t.Run("returns the set value", func(t *testing.T) {
		t.Setenv("ENV_TEST_GET", "hello")
		if got := Get("ENV_TEST_GET"); got != "hello" {
			t.Errorf("Get() = %q, want %q", got, "hello")
		}
	})

	t.Run("falls back to the default when unset", func(t *testing.T) {
		t.Parallel()
		if got := Get("ENV_TEST_GET_UNSET", "fallback"); got != "fallback" {
			t.Errorf("Get() = %q, want %q", got, "fallback")
		}
	})

	t.Run("returns empty string when unset with no default", func(t *testing.T) {
		t.Parallel()
		if got := Get("ENV_TEST_GET_UNSET"); got != "" {
			t.Errorf("Get() = %q, want empty", got)
		}
	})

	t.Run("applies castString special cases", func(t *testing.T) {
		t.Setenv("ENV_TEST_GET_NULL", "null")
		if got := Get("ENV_TEST_GET_NULL"); got != "" {
			t.Errorf(`Get() with "null" = %q, want empty`, got)
		}
	})
}

func TestHas(t *testing.T) {
	t.Run("true when set, even to empty", func(t *testing.T) {
		t.Setenv("ENV_TEST_HAS", "")
		if !Has("ENV_TEST_HAS") {
			t.Error("Has() = false, want true for a var set to empty string")
		}
	})

	t.Run("false when unset", func(t *testing.T) {
		t.Parallel()
		if Has("ENV_TEST_HAS_UNSET") {
			t.Error("Has() = true, want false")
		}
	})
}

func TestMustGet(t *testing.T) {
	t.Run("returns the value when set", func(t *testing.T) {
		t.Setenv("ENV_TEST_MUSTGET", "value")
		if got := MustGet("ENV_TEST_MUSTGET"); got != "value" {
			t.Errorf("MustGet() = %q, want %q", got, "value")
		}
	})

	t.Run("panics when unset", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Error("MustGet() did not panic on an unset variable")
			}
		}()
		MustGet("ENV_TEST_MUSTGET_UNSET")
	})
}

func TestGetInt(t *testing.T) {
	const unsetKey = "ENV_TEST_GETINT_DOES_NOT_EXIST"

	tests := []struct {
		name       string
		value      string
		set        bool
		defaultVal []int
		want       int
	}{
		{"parses a valid int", "42", true, nil, 42},
		{"trims whitespace", "  7  ", true, nil, 7},
		{"falls back on invalid input", "not-a-number", true, []int{9}, 9},
		{"falls back when unset", "", false, []int{5}, 5},
		{"zero value when unset with no default", "", false, nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: some cases call t.Setenv, and mixing that with
			// t.Parallel() in the same subtest body is unsafe/disallowed.
			key := unsetKey
			if tt.set {
				key = "ENV_TEST_GETINT"
				t.Setenv(key, tt.value)
			}
			if got := GetInt(key, tt.defaultVal...); got != tt.want {
				t.Errorf("GetInt() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetFloat(t *testing.T) {
	t.Run("parses a valid float", func(t *testing.T) {
		t.Setenv("ENV_TEST_GETFLOAT", "3.14")
		if got := GetFloat("ENV_TEST_GETFLOAT"); got != 3.14 {
			t.Errorf("GetFloat() = %v, want %v", got, 3.14)
		}
	})

	t.Run("falls back on invalid input", func(t *testing.T) {
		t.Setenv("ENV_TEST_GETFLOAT_BAD", "nope")
		if got := GetFloat("ENV_TEST_GETFLOAT_BAD", 1.5); got != 1.5 {
			t.Errorf("GetFloat() = %v, want %v", got, 1.5)
		}
	})
}

func TestGetBool(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		defaultVal []bool
		want       bool
	}{
		{"true", "true", nil, true},
		{"1", "1", nil, true},
		{"yes", "yes", nil, true},
		{"on (case-insensitive)", "ON", nil, true},
		{"false", "false", nil, false},
		{"0", "0", nil, false},
		{"no", "no", nil, false},
		{"off", "off", nil, false},
		{"empty string falls back to default, not false", "", []bool{true}, true},
		{"empty string with no default is zero value", "", nil, false},
		{"garbage falls back to default", "maybe", []bool{true}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: every case calls t.Setenv.
			t.Setenv("ENV_TEST_GETBOOL", tt.value)
			if got := GetBool("ENV_TEST_GETBOOL", tt.defaultVal...); got != tt.want {
				t.Errorf("GetBool(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestGetSlice(t *testing.T) {
	t.Run("splits and trims on the default separator", func(t *testing.T) {
		t.Setenv("ENV_TEST_GETSLICE", "a, b ,c")
		got := GetSlice("ENV_TEST_GETSLICE", "")
		want := []string{"a", "b", "c"}
		if !equalSlices(got, want) {
			t.Errorf("GetSlice() = %v, want %v", got, want)
		}
	})

	t.Run("drops empty elements", func(t *testing.T) {
		t.Setenv("ENV_TEST_GETSLICE_EMPTY", "a,,b,")
		got := GetSlice("ENV_TEST_GETSLICE_EMPTY", "")
		want := []string{"a", "b"}
		if !equalSlices(got, want) {
			t.Errorf("GetSlice() = %v, want %v", got, want)
		}
	})

	t.Run("uses a custom separator", func(t *testing.T) {
		t.Setenv("ENV_TEST_GETSLICE_SEP", "a|b|c")
		got := GetSlice("ENV_TEST_GETSLICE_SEP", "|")
		want := []string{"a", "b", "c"}
		if !equalSlices(got, want) {
			t.Errorf("GetSlice() = %v, want %v", got, want)
		}
	})

	t.Run("falls back when unset", func(t *testing.T) {
		t.Parallel()
		got := GetSlice("ENV_TEST_GETSLICE_UNSET", ",", []string{"fallback"})
		want := []string{"fallback"}
		if !equalSlices(got, want) {
			t.Errorf("GetSlice() = %v, want %v", got, want)
		}
	})
}

func TestEnvironment(t *testing.T) {
	t.Run("defaults to production", func(t *testing.T) {
		t.Parallel()
		if got := Environment(); got != "production" {
			t.Errorf("Environment() = %q, want %q", got, "production")
		}
	})

	t.Run("reflects APP_ENV", func(t *testing.T) {
		t.Setenv("APP_ENV", "local")
		if got := Environment(); got != "local" {
			t.Errorf("Environment() = %q, want %q", got, "local")
		}
	})
}

func TestIs(t *testing.T) {
	t.Run("matches case-insensitively against any given name", func(t *testing.T) {
		t.Setenv("APP_ENV", "Local")
		if !Is("production", "local") {
			t.Error("Is(production, local) = false, want true")
		}
		if Is("staging") {
			t.Error("Is(staging) = true, want false")
		}
	})

	t.Run("IsProduction/IsLocal/IsTesting reflect APP_ENV", func(t *testing.T) {
		t.Setenv("APP_ENV", "testing")
		if IsProduction() {
			t.Error("IsProduction() = true, want false")
		}
		if IsLocal() {
			t.Error("IsLocal() = true, want false")
		}
		if !IsTesting() {
			t.Error("IsTesting() = false, want true")
		}
	})
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
