package media

import "testing"

const sampleProbeJSON = `{
  "streams": [
    {"index":0,"codec_name":"h264","codec_type":"video","profile":"High","width":1920,"height":1080,"pix_fmt":"yuv420p","bit_rate":"4096000","avg_frame_rate":"25/1"},
    {"index":1,"codec_name":"aac","codec_type":"audio","sample_rate":"44100","channels":2,"channel_layout":"stereo"}
  ],
  "format": {"format_name":"rtsp","duration":"N/A","bit_rate":"4200000"}
}`

func TestParseProbeJSON(t *testing.T) {
	res, err := ParseProbeJSON([]byte(sampleProbeJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Format != "rtsp" || res.BitRate != 4200000 || res.Streams != 2 {
		t.Fatalf("format: %+v", res)
	}
	if res.Video == nil || res.Video.Width != 1920 || res.Video.Height != 1080 || res.Video.FrameRate != 25 {
		t.Fatalf("video: %+v", res.Video)
	}
	if res.Audio == nil || res.Audio.Channels != 2 || res.Audio.SampleRate != 44100 {
		t.Fatalf("audio: %+v", res.Audio)
	}
}

func TestParseProbeJSONFrameRateFraction(t *testing.T) {
	res, err := ParseProbeJSON([]byte(`{"streams":[{"index":0,"codec_name":"h264","codec_type":"video","width":640,"height":360,"avg_frame_rate":"30000/1001"}],"format":{"format_name":"flv"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := res.Video.FrameRate; got < 29.9 || got > 30.0 {
		t.Fatalf("frame rate = %v", got)
	}
}

func TestParseProbeJSONNoStreams(t *testing.T) {
	if _, err := ParseProbeJSON([]byte(`{"streams":[],"format":{}}`)); err == nil {
		t.Fatal("expected error when no av streams")
	}
}
