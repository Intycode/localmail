package queue

import "testing"

func TestBackoffNeverOverflows(t *testing.T) {
	for _, n := range []int{0, 5, 29, 30, 64, 1000} {
		if d := Backoff(n); d <= 0 || d > maxDelay {
			t.Fatalf("Backoff(%d) = %v", n, d)
		}
	}
}
