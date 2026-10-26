package video

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// Player URLs must follow the page scheme: an https page cannot load http
// media, so a TLS-fronted deployment points the platform at https + 443 and
// proxies those paths back to the plain-http media daemon.
func TestPlayURLsScheme(t *testing.T) {
	c := NewClient("http://127.0.0.1:8080", "s", "app.example.com", 443, 443, 554, 1935)
	c.Scheme = "https"
	urls := c.PlayURLs("ch_x")
	for _, proto := range []string{"hls", "http-flv", "http-fmp4", "webrtc"} {
		if !strings.HasPrefix(urls[proto], "https://app.example.com/") {
			t.Errorf("%s = %q, want https on the default port", proto, urls[proto])
		}
	}
	if urls["rtsp"] != "rtsp://app.example.com:554/live/ch_x" {
		t.Errorf("rtsp = %q", urls["rtsp"])
	}
	if got := c.RecordURL("ch_x", "2026-10-24", "a.mp4"); got != "https://app.example.com/record/live/ch_x/2026-10-24/a.mp4" {
		t.Errorf("RecordURL = %q", got)
	}

	plain := NewClient("http://127.0.0.1:80", "s", "127.0.0.1", 80, 80, 554, 1935)
	if got := plain.PlayURLs("ch_x")["hls"]; got != "http://127.0.0.1/live/ch_x/hls.m3u8" {
		t.Errorf("default scheme hls = %q", got)
	}
}

// DeleteRecordDir is POST-only on this ZLM build; the client must use the form
// endpoint and surface 404 as an error (the endpoint name differs across
// ZLM releases, and a silent no-op would leave media files behind).
func TestDeleteRecordDir(t *testing.T) {
	var got rtspRecorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err == nil {
				got.stream = r.FormValue("stream")
				got.period = r.FormValue("period")
			}
		}
		if r.URL.Query().Get("fail") == "404" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "s", "127.0.0.1", 80, 8080, 554, 1935)
	if err := c.DeleteRecordDir("ch_x", "2026-10-24"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got.method != http.MethodPost || !strings.HasSuffix(got.path, "/index/api/deleteRecordDirectory") {
		t.Fatalf("request = %s %s, want POST .../deleteRecordDirectory", got.method, got.path)
	}
	if got.stream != "ch_x" || got.period != "2026-10-24" {
		t.Fatalf("params stream=%q period=%q", got.stream, got.period)
	}
}

type rtspRecorder struct{ method, path, stream, period string }

func TestDeleteRecordDirUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fail") != "" {
			// nothing
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := NewClient(srv.URL+"?fail=1", "s", "127.0.0.1", 80, 8080, 554, 1935)
	// PostForm to "URL?fail=1" appends form params; the handler 404s when the
	// fail marker is present, which is what an older ZLM (missing endpoint) does.
	if err := c.DeleteRecordDir("ch_x", "2026-10-24"); err == nil {
		t.Fatal("expected error when ZLM returns 404 for the delete endpoint")
	}
}
