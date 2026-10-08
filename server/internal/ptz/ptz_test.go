package ptz

import "testing"

func checksum(b []byte) byte {
	var sum int
	for i := 0; i < 7; i++ {
		sum += int(b[i])
	}
	return byte(sum % 256)
}

func TestBuildGB28181Cmd(t *testing.T) {
	cases := []struct {
		cmd     string
		speed   int
		ctrl    byte
		pan     byte
		tilt    byte
		zoom    byte
		wantErr bool
	}{
		{cmd: CmdRight, speed: 100, ctrl: 0x01, pan: 100},
		{cmd: CmdLeft, speed: 0x30, ctrl: 0x02, pan: 0x30},
		{cmd: CmdUp, speed: 0x30, ctrl: 0x08, tilt: 0x30},
		{cmd: CmdDown, speed: 0x30, ctrl: 0x04, tilt: 0x30},
		{cmd: CmdUpLeft, speed: 0x30, ctrl: 0x0A, pan: 0x30, tilt: 0x30},
		{cmd: CmdDownRight, speed: 0x30, ctrl: 0x05, pan: 0x30, tilt: 0x30},
		{cmd: CmdZoomIn, speed: 0x30, ctrl: 0x10, zoom: 0x30},
		{cmd: CmdZoomOut, speed: 0x30, ctrl: 0x20, zoom: 0x30},
		{cmd: CmdStop, speed: 100, ctrl: 0x00},
		{cmd: "bogus", speed: 10, wantErr: true},
	}
	for _, tc := range cases {
		b, hexs, err := BuildGB28181Cmd(tc.cmd, tc.speed)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%s: expected error", tc.cmd)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.cmd, err)
		}
		if b[0] != 0xA5 || b[1] != 0x0F || b[2] != 0x01 {
			t.Fatalf("%s: bad header % X", tc.cmd, b)
		}
		if b[6] != tc.ctrl {
			t.Fatalf("%s: ctrl = %#x want %#x", tc.cmd, b[6], tc.ctrl)
		}
		if b[3] != tc.pan || b[4] != tc.tilt || b[5] != tc.zoom {
			t.Fatalf("%s: speeds % X want pan=%d tilt=%d zoom=%d", tc.cmd, b, tc.pan, tc.tilt, tc.zoom)
		}
		if b[7] != checksum(b) {
			t.Fatalf("%s: checksum %#x", tc.cmd, b[7])
		}
		if len(hexs) != 16 {
			t.Fatalf("%s: hex %q", tc.cmd, hexs)
		}
	}
}

func TestBuildPresetCmd(t *testing.T) {
	b, _, err := BuildPresetCmd(PresetGoto, 5)
	if err != nil {
		t.Fatalf("goto: %v", err)
	}
	if b[3] != 5 || b[6] != 0x82 {
		t.Fatalf("unexpected goto preset cmd: % X", b)
	}
	if b[7] != checksum(b) {
		t.Fatalf("goto checksum")
	}
	if _, _, err := BuildPresetCmd(PresetSet, 0); err == nil {
		t.Fatal("preset 0 should be rejected")
	}
	if _, _, err := BuildPresetCmd("nope", 1); err == nil {
		t.Fatal("bad action should be rejected")
	}
}

func TestPanTiltZoom(t *testing.T) {
	pan, tilt, zoom, err := PanTiltZoom(CmdUpRight, 80)
	if err != nil || pan != 80 || tilt != 80 || zoom != 0 {
		t.Fatalf("up_right = %d,%d,%d err=%v", pan, tilt, zoom, err)
	}
	pan, tilt, zoom, _ = PanTiltZoom(CmdZoomOut, 150)
	if pan != 0 || tilt != 0 || zoom != -100 {
		t.Fatalf("zoom out should clamp to -100: %d,%d,%d", pan, tilt, zoom)
	}
	if !Supported(CmdDownLeft) || Supported("bogus") {
		t.Fatal("Supported() mismatch")
	}
}
