package main

import "time"

func isQuietHours(t time.Time) bool {
	hour := t.Hour()
	if hour >= 22 || hour < 9 {
		return true
	}
	return hour >= 13 && hour < 15
}
