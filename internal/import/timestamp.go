package importer

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseTimestamp parses a timestamp string in various formats.
// Supports RFC3339, common date formats, Slack epoch.micro format,
// and relative time expressions (e.g., "2h ago", "3d ago").
func ParseTimestamp(ts string) (time.Time, error) {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}

	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t, nil
	}

	// Try RFC3339Nano
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t, nil
	}

	// Try common date-time formats
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"Jan 2, 2006 3:04 PM",
		"Jan 2, 2006",
		"1/2/2006",
		"01/02/2006",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, ts); err == nil {
			return t, nil
		}
	}

	// Try Slack epoch.micro format (e.g., "1706745600.123456")
	if parts := strings.Split(ts, "."); len(parts) >= 1 {
		if epoch, err := strconv.ParseInt(parts[0], 10, 64); err == nil && epoch > 1000000000 && epoch < 9999999999 {
			return time.Unix(epoch, 0), nil
		}
	}

	// Try relative time expressions (e.g., "2h ago", "1d ago", "1w ago")
	if strings.HasSuffix(ts, " ago") {
		durationStr := strings.TrimSuffix(ts, " ago")
		durationStr = strings.TrimSpace(durationStr)

		// Handle day shortcuts
		if strings.HasSuffix(durationStr, "d") {
			numStr := strings.TrimSuffix(durationStr, "d")
			if days, err := strconv.Atoi(numStr); err == nil {
				return time.Now().Add(-time.Duration(days) * 24 * time.Hour), nil
			}
		}

		// Handle week shortcuts
		if strings.HasSuffix(durationStr, "w") {
			numStr := strings.TrimSuffix(durationStr, "w")
			if weeks, err := strconv.Atoi(numStr); err == nil {
				return time.Now().Add(-time.Duration(weeks) * 7 * 24 * time.Hour), nil
			}
		}

		// Try standard Go duration
		if d, err := time.ParseDuration(durationStr); err == nil {
			return time.Now().Add(-d), nil
		}
	}

	return time.Time{}, fmt.Errorf("unrecognized timestamp format: %s", ts)
}
