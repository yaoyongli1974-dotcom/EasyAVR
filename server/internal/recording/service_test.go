package recording

import (
	"testing"
	"time"

	"github.com/easyavr/easyavr/internal/model"
)

func TestPlanMatches(t *testing.T) {
	// Monday 10:00.
	mon := time.Date(2024, 1, 1, 10, 0, 0, 0, time.Local)
	if mon.Weekday() != time.Monday {
		t.Fatalf("fixture weekday = %v", mon.Weekday())
	}

	cases := []struct {
		name string
		plan model.RecordingPlan
		now  time.Time
		want bool
	}{
		{"daily inside", model.RecordingPlan{Days: "daily", StartTime: "08:00", EndTime: "20:00"}, mon, true},
		{"daily outside", model.RecordingPlan{Days: "daily", StartTime: "11:00", EndTime: "20:00"}, mon, false},
		{"workday monday", model.RecordingPlan{Days: "workday", StartTime: "00:00", EndTime: "23:59"}, mon, true},
		{"weekend monday", model.RecordingPlan{Days: "weekend", StartTime: "00:00", EndTime: "23:59"}, mon, false},
		{"explicit monday", model.RecordingPlan{Days: "1,3,5", StartTime: "00:00", EndTime: "23:59"}, mon, true},
		{"overnight before midnight", model.RecordingPlan{Days: "daily", StartTime: "22:00", EndTime: "06:00"}, mon.Add(13 * time.Hour), true}, // 23:00
		{"overnight after midnight", model.RecordingPlan{Days: "daily", StartTime: "22:00", EndTime: "06:00"}, time.Date(2024, 1, 1, 2, 0, 0, 0, time.Local), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := planMatches(tc.plan, tc.now); got != tc.want {
				t.Fatalf("planMatches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseRecordTime(t *testing.T) {
	got := parseRecordTime("2024-01-02", "2024-01-02/14-30-05.mp4")
	want := time.Date(2024, 1, 2, 14, 30, 5, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("parseRecordTime = %v, want %v", got, want)
	}
}
