package importer

import (
	"testing"
	"time"
)

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		checkFunc func(t *testing.T, got time.Time)
	}{
		{
			name:  "RFC3339",
			input: "2024-06-15T10:00:00Z",
			checkFunc: func(t *testing.T, got time.Time) {
				expected := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
				if !got.Equal(expected) {
					t.Errorf("got %v, want %v", got, expected)
				}
			},
		},
		{
			name:  "RFC3339Nano",
			input: "2024-06-15T10:00:00.123456789Z",
			checkFunc: func(t *testing.T, got time.Time) {
				if got.Year() != 2024 || got.Month() != 6 || got.Day() != 15 {
					t.Errorf("got %v, want 2024-06-15", got)
				}
				if got.Nanosecond() != 123456789 {
					t.Errorf("got nanosecond %d, want 123456789", got.Nanosecond())
				}
			},
		},
		{
			name:  "date-time no TZ",
			input: "2024-06-15T10:00:00",
			checkFunc: func(t *testing.T, got time.Time) {
				if got.Year() != 2024 || got.Month() != 6 || got.Day() != 15 {
					t.Errorf("got %v, want 2024-06-15", got)
				}
			},
		},
		{
			name:  "date-time with space",
			input: "2024-06-15 10:00:00",
			checkFunc: func(t *testing.T, got time.Time) {
				if got.Year() != 2024 || got.Month() != 6 || got.Day() != 15 {
					t.Errorf("got %v, want 2024-06-15", got)
				}
			},
		},
		{
			name:  "date only",
			input: "2024-06-15",
			checkFunc: func(t *testing.T, got time.Time) {
				if got.Year() != 2024 || got.Month() != 6 || got.Day() != 15 {
					t.Errorf("got %v, want 2024-06-15", got)
				}
			},
		},
		{
			name:  "US date m/d/yyyy",
			input: "1/2/2006",
			checkFunc: func(t *testing.T, got time.Time) {
				if got.Year() != 2006 || got.Month() != 1 || got.Day() != 2 {
					t.Errorf("got %v, want 2006-01-02", got)
				}
			},
		},
		{
			name:  "US date mm/dd/yyyy",
			input: "01/02/2006",
			checkFunc: func(t *testing.T, got time.Time) {
				if got.Year() != 2006 || got.Month() != 1 || got.Day() != 2 {
					t.Errorf("got %v, want 2006-01-02", got)
				}
			},
		},
		{
			name:  "English date with time",
			input: "Jan 2, 2006 3:04 PM",
			checkFunc: func(t *testing.T, got time.Time) {
				if got.Year() != 2006 || got.Month() != 1 || got.Day() != 2 {
					t.Errorf("got %v, want 2006-01-02", got)
				}
			},
		},
		{
			name:  "English date no time",
			input: "Jan 2, 2006",
			checkFunc: func(t *testing.T, got time.Time) {
				if got.Year() != 2006 || got.Month() != 1 || got.Day() != 2 {
					t.Errorf("got %v, want 2006-01-02", got)
				}
			},
		},
		{
			name:  "Slack epoch",
			input: "1706745600.123456",
			checkFunc: func(t *testing.T, got time.Time) {
				// 1706745600 = 2024-02-01 00:00:00 UTC
				expected := time.Unix(1706745600, 0)
				if !got.Equal(expected) {
					t.Errorf("got %v, want %v", got, expected)
				}
			},
		},
		{
			name:  "relative hours",
			input: "2h ago",
			checkFunc: func(t *testing.T, got time.Time) {
				expected := time.Now().Add(-2 * time.Hour)
				diff := expected.Sub(got).Abs()
				if diff > 5*time.Second {
					t.Errorf("got %v, expected within 5s of %v (diff: %v)", got, expected, diff)
				}
			},
		},
		{
			name:  "relative days",
			input: "3d ago",
			checkFunc: func(t *testing.T, got time.Time) {
				expected := time.Now().Add(-3 * 24 * time.Hour)
				diff := expected.Sub(got).Abs()
				if diff > 5*time.Second {
					t.Errorf("got %v, expected within 5s of %v (diff: %v)", got, expected, diff)
				}
			},
		},
		{
			name:  "relative weeks",
			input: "1w ago",
			checkFunc: func(t *testing.T, got time.Time) {
				expected := time.Now().Add(-7 * 24 * time.Hour)
				diff := expected.Sub(got).Abs()
				if diff > 5*time.Second {
					t.Errorf("got %v, expected within 5s of %v (diff: %v)", got, expected, diff)
				}
			},
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "garbage",
			input:   "not-a-timestamp",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTimestamp(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseTimestamp(%q) expected error, got %v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTimestamp(%q) unexpected error: %v", tt.input, err)
			}
			if tt.checkFunc != nil {
				tt.checkFunc(t, got)
			}
		})
	}
}
