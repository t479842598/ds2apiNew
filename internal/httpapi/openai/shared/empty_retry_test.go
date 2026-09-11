package shared

import "testing"

func TestEmptyOutputRetryMaxAttemptsDefaults(t *testing.T) {
	t.Setenv("DS2API_EMPTY_OUTPUT_RETRY_MAX_ATTEMPTS", "")
	if got := EmptyOutputRetryMaxAttempts(); got != DefaultEmptyOutputRetryMaxAttempts {
		t.Fatalf("expected default %d, got %d", DefaultEmptyOutputRetryMaxAttempts, got)
	}
}

func TestEmptyOutputRetryMaxAttemptsOverride(t *testing.T) {
	t.Setenv("DS2API_EMPTY_OUTPUT_RETRY_MAX_ATTEMPTS", "5")
	if got := EmptyOutputRetryMaxAttempts(); got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
}

func TestEmptyOutputRetryMaxAttemptsFallsBackOnInvalid(t *testing.T) {
	for _, raw := range []string{"abc", "0", "-1", "3abc", "  "} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("DS2API_EMPTY_OUTPUT_RETRY_MAX_ATTEMPTS", raw)
			if got := EmptyOutputRetryMaxAttempts(); got != DefaultEmptyOutputRetryMaxAttempts {
				t.Fatalf("raw %q: expected fallback %d, got %d", raw, DefaultEmptyOutputRetryMaxAttempts, got)
			}
		})
	}
}

func TestEmptyOutputRetryMaxAttemptsAllowsOne(t *testing.T) {
	// Operators must be able to restore the pre-hardening behaviour via env.
	t.Setenv("DS2API_EMPTY_OUTPUT_RETRY_MAX_ATTEMPTS", "1")
	if got := EmptyOutputRetryMaxAttempts(); got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}
