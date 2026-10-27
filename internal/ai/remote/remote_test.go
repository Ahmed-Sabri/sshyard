package remote

import (
	"context"
	"testing"
	"time"
)

func TestCommandTimeoutClamp(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{name: "default", want: DefaultCommandTimeout},
		{name: "below min", in: time.Millisecond, want: MinCommandTimeout},
		{name: "min", in: MinCommandTimeout, want: MinCommandTimeout},
		{name: "inside", in: time.Minute, want: time.Minute},
		{name: "above max", in: 31 * time.Minute, want: MaxCommandTimeout},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandTimeout(tc.in); got != tc.want {
				t.Fatalf("%s -> %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestRunNilClient(t *testing.T) {
	_, err := New(nil, Options{}).Run(context.Background(), "echo", "", nil, nil)
	if err == nil {
		t.Fatal("nil client")
	}
}
