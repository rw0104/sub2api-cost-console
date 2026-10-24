package service

import (
	"errors"
	"time"
)

var ErrInvalidEconomicsWindow = errors.New("invalid account economics window")

// ResolveEconomicsWindow retains duration-only clients while making explicit
// client calendar boundaries authoritative. Future time is never accrued.
func ResolveEconomicsWindow(query AccountEconomicsQuery, now time.Time) (time.Time, time.Time, *time.Location, error) {
	location := time.UTC
	if query.Timezone != "" {
		var err error
		location, err = time.LoadLocation(query.Timezone)
		if err != nil {
			return time.Time{}, time.Time{}, nil, ErrInvalidEconomicsWindow
		}
	}
	start, end := query.StartTime, query.EndTime
	if start.IsZero() != end.IsZero() {
		return time.Time{}, time.Time{}, nil, ErrInvalidEconomicsWindow
	}
	if start.IsZero() {
		window := query.Window
		if window == 0 {
			window = time.Hour
		}
		if window < 0 || window > 30*24*time.Hour {
			return time.Time{}, time.Time{}, nil, ErrInvalidEconomicsWindow
		}
		start, end = now.Add(-window), now
	}
	if end.Before(start) || end.Sub(start) > 30*24*time.Hour || start.After(now) {
		return time.Time{}, time.Time{}, nil, ErrInvalidEconomicsWindow
	}
	if end.After(now) {
		end = now
	}
	return start.UTC(), end.UTC(), location, nil
}
