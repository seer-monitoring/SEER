package main

import (
	"math"
	"net/http"
	"testing"
	"time"
)

func TestComputeBackoffDelayBounds(t *testing.T) {
	base := time.Second
	max := 30 * time.Second
	for attempt := 0; attempt < 8; attempt++ {
		ceiling := time.Duration(float64(base) * math.Pow(2, float64(attempt)))
		if ceiling > max {
			ceiling = max
		}
		for i := 0; i < 20; i++ {
			d := computeBackoffDelay(attempt, base, max, nil)
			if d < 0 || d > ceiling {
				t.Fatalf("attempt=%d delay=%v out of [0, %v]", attempt, d, ceiling)
			}
		}
	}
}

func TestComputeBackoffDelayRetryAfter(t *testing.T) {
	resp := &http.Response{
		StatusCode: 429,
		Header:     http.Header{"Retry-After": []string{"3"}},
	}
	d := computeBackoffDelay(0, time.Second, 30*time.Second, resp)
	if d != 3*time.Second {
		t.Fatalf("expected 3s from Retry-After, got %v", d)
	}
}
