package domain

import (
	"testing"
	"time"
)

func TestGroundEvidenceSeparatesEarlierAndLaterMotion(t *testing.T) {
	boundary := time.Unix(100, 500000)
	for _, kind := range []string{"ARM", "RESUME", "MISSION_START", "DISARM", "LAND", "RTL", "PAUSE", "MISSION_UPLOAD"} {
		risky := kind == "ARM" || kind == "RESUME" || kind == "MISSION_START"
		for _, offset := range []time.Duration{-time.Millisecond, 0, time.Millisecond} {
			c := Command{Type: kind, State: "applied", Events: []CommandEvent{{Stage: "applied", OccurredAt: boundary.Add(offset)}}}
			if got, want := c.InvalidatesGroundEvidence(boundary), risky && offset >= 0; got != want {
				t.Fatalf("%s offset=%s got=%v want=%v", kind, offset, got, want)
			}
		}
	}
}
