package video

import (
	"strings"
	"testing"
)

func TestSummarizeStreams(t *testing.T) {
	// ZLM reports one entry per schema for the same stream; only totalBytes and
	// totalReaderCount are stream-wide.
	streams := []MediaStream{
		{Stream: "ch_a", Schema: "hls", TotalBytes: 0, ReaderCount: 0, AliveSecond: 5, TotalReaderCount: 2},
		{Stream: "ch_a", Schema: "rtsp", TotalBytes: 1942416, ReaderCount: 2, AliveSecond: 5, TotalReaderCount: 2},
		{Stream: "ch_a", Schema: "rtmp", TotalBytes: 1942416, ReaderCount: 1, AliveSecond: 5, TotalReaderCount: 2},
		{Stream: "ch_b", Schema: "rtsp", TotalBytes: 100, ReaderCount: 1, AliveSecond: 9, TotalReaderCount: 1},
	}
	got := SummarizeStreams(streams)
	if len(got) != 2 {
		t.Fatalf("streams summarized = %d, want 2", len(got))
	}
	a := got["ch_a"]
	if a.TotalBytes != 1942416 {
		t.Errorf("ch_a bytes = %d, want 1942416", a.TotalBytes)
	}
	if a.ReaderCount != 3 {
		t.Errorf("ch_a readers = %d, want 3", a.ReaderCount)
	}
	if a.TotalReaderCount != 2 {
		t.Errorf("ch_a total readers = %d, want 2", a.TotalReaderCount)
	}
	if b := got["ch_b"]; b.TotalBytes != 100 || b.ReaderCount != 1 {
		t.Errorf("ch_b = %+v", b)
	}
}

func TestRecordURL(t *testing.T) {
	c := NewClient("http://127.0.0.1:80", "s", "127.0.0.1", 80, 8080, 554, 1935)
	cases := []struct{ name, date, path, want string }{
		{
			"bare file name",
			"2026-10-24", "2026-10-24-15-57-03-0.mp4",
			"http://127.0.0.1/record/live/ch_x/2026-10-24/2026-10-24-15-57-03-0.mp4",
		},
		{
			"already dated path",
			"2026-10-24", "2026-10-24/15-57-03.mp4",
			"http://127.0.0.1/record/live/ch_x/2026-10-24/15-57-03.mp4",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.RecordURL("ch_x", tc.date, tc.path)
			if got != tc.want {
				t.Fatalf("RecordURL = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "//record") {
				t.Fatalf("malformed url %q", got)
			}
		})
	}
}
