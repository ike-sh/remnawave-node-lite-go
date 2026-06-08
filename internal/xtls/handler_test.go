package xtls

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsUserNotFound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"not found code", status.Error(codes.NotFound, "user missing"), true},
		{"not found message", status.Error(codes.Unknown, "user not found in inbound"), true},
		{"other", status.Error(codes.Internal, "boom"), false},
		{"plain", errors.New("not exist"), false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isUserNotFound(tc.err); got != tc.want {
				t.Fatalf("isUserNotFound() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsUserExists(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"already exists", status.Error(codes.AlreadyExists, "user already exists"), true},
		{"duplicate", status.Error(codes.Unknown, "duplicate user email"), true},
		{"other", status.Error(codes.Internal, "boom"), false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isUserExists(tc.err); got != tc.want {
				t.Fatalf("isUserExists() = %v, want %v", got, tc.want)
			}
		})
	}
}
