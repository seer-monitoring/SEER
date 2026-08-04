package config

import "testing"

func TestResolveHTTPAddr(t *testing.T) {
	t.Setenv("SEER_HTTP_ADDR", "")
	t.Setenv("SEER_PORT", "")
	if got := resolveHTTPAddr(); got != ":8080" {
		t.Fatalf("default: got %q", got)
	}

	t.Setenv("SEER_PORT", "9090")
	if got := resolveHTTPAddr(); got != ":9090" {
		t.Fatalf("SEER_PORT: got %q", got)
	}

	t.Setenv("SEER_PORT", ":7070")
	if got := resolveHTTPAddr(); got != ":7070" {
		t.Fatalf("SEER_PORT with colon: got %q", got)
	}

	t.Setenv("SEER_HTTP_ADDR", ":6060")
	t.Setenv("SEER_PORT", "9090")
	if got := resolveHTTPAddr(); got != ":6060" {
		t.Fatalf("SEER_HTTP_ADDR should win: got %q", got)
	}
}
