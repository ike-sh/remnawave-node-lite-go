//go:build linux

package instance

import "testing"

func TestAcquireDetectsDuplicateAndReleases(t *testing.T) {
	first, acquired, err := Acquire()
	if err != nil || !acquired || first == nil {
		t.Fatalf("first Acquire() = (%v, %v, %v), want lock, true, nil", first, acquired, err)
	}

	second, acquired, err := Acquire()
	if err != nil || acquired || second != nil {
		_ = first.Close()
		t.Fatalf("second Acquire() = (%v, %v, %v), want nil, false, nil", second, acquired, err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	third, acquired, err := Acquire()
	if err != nil || !acquired || third == nil {
		t.Fatalf("third Acquire() after release = (%v, %v, %v), want lock, true, nil", third, acquired, err)
	}
	_ = third.Close()
}
