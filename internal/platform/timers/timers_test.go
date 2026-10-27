package timers

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{-1, 10 * time.Second},
		{0, 10 * time.Second},
		{1, 20 * time.Second},
		{5, 320 * time.Second},
		{8, 2560 * time.Second},
		{9, time.Hour},
		{100, time.Hour},
	}
	for _, tt := range tests {
		if got := Backoff(tt.attempts); got != tt.want {
			t.Errorf("Backoff(%d) = %v, want %v", tt.attempts, got, tt.want)
		}
	}
}
