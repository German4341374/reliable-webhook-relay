package worker

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	t.Parallel()
	base := time.Second
	maximum := 10 * time.Second
	tests := []struct {
		name    string
		attempt int
		jitter  float64
		want    time.Duration
	}{
		{name: "first without jitter", attempt: 1, jitter: 0, want: time.Second},
		{name: "second without jitter", attempt: 2, jitter: 0, want: 2 * time.Second},
		{name: "third with maximum jitter", attempt: 3, jitter: 1, want: 5 * time.Second},
		{name: "capped", attempt: 8, jitter: 1, want: 10 * time.Second},
		{name: "invalid attempt becomes first", attempt: 0, jitter: 0, want: time.Second},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Backoff(base, maximum, test.attempt, test.jitter); got != test.want {
				t.Fatalf("Backoff() = %s, want %s", got, test.want)
			}
		})
	}
}
