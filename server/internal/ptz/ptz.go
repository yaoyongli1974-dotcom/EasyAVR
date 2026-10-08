// Package ptz encodes pan/tilt/zoom (云台) control commands for the supported
// device protocols. GB28181 uses the 8-byte PTZ command defined in
// GB/T 28181-2016 Appendix A.3; ISAPI uses HTTP with signed pan/tilt/zoom
// values. Keeping the encoding here makes it unit-testable.
package ptz

import (
	"fmt"
	"strings"
)

// Direction command names accepted by the API.
const (
	CmdUp        = "up"
	CmdDown      = "down"
	CmdLeft      = "left"
	CmdRight     = "right"
	CmdUpLeft    = "up_left"
	CmdUpRight   = "up_right"
	CmdDownLeft  = "down_left"
	CmdDownRight = "down_right"
	CmdZoomIn    = "zoom_in"
	CmdZoomOut   = "zoom_out"
	CmdStop      = "stop"
)

// direction bit flags in the GB28181 control byte (byte 7, 0-indexed 6).
var directionBits = map[string]byte{
	CmdRight:   0x01,
	CmdLeft:    0x02,
	CmdDown:    0x04,
	CmdUp:      0x08,
	CmdZoomIn:  0x10,
	CmdZoomOut: 0x20,
}

var combos = map[string][]string{
	CmdUp:        {CmdUp},
	CmdDown:      {CmdDown},
	CmdLeft:      {CmdLeft},
	CmdRight:     {CmdRight},
	CmdUpLeft:    {CmdUp, CmdLeft},
	CmdUpRight:   {CmdUp, CmdRight},
	CmdDownLeft:  {CmdDown, CmdLeft},
	CmdDownRight: {CmdDown, CmdRight},
	CmdZoomIn:    {CmdZoomIn},
	CmdZoomOut:   {CmdZoomOut},
	CmdStop:      {},
}

// Supported reports whether cmd is a known PTZ command.
func Supported(cmd string) bool {
	_, ok := combos[strings.TrimSpace(cmd)]
	return ok
}

func clampSpeed(speed int) byte {
	if speed <= 0 {
		return 0
	}
	if speed > 255 {
		return 255
	}
	return byte(speed)
}

// BuildGB28181Cmd builds the 8-byte GB28181 PTZ command and its hex form.
func BuildGB28181Cmd(cmd string, speed int) ([]byte, string, error) {
	cmd = strings.TrimSpace(cmd)
	parts, ok := combos[cmd]
	if !ok {
		return nil, "", fmt.Errorf("unsupported ptz cmd %q", cmd)
	}
	var ctrl byte
	var pan, tilt, zoom byte
	sp := clampSpeed(speed)
	if sp == 0 {
		sp = 0x30 // a gentle default when the caller omits a speed
	}
	for _, p := range parts {
		ctrl |= directionBits[p]
		switch p {
		case CmdLeft, CmdRight:
			pan = sp
		case CmdUp, CmdDown:
			tilt = sp
		case CmdZoomIn, CmdZoomOut:
			zoom = sp
		}
	}
	if cmd == CmdStop {
		pan, tilt, zoom, ctrl = 0, 0, 0, 0
	}
	b := []byte{0xA5, 0x0F, 0x01, pan, tilt, zoom, ctrl, 0}
	var sum int
	for i := 0; i < 7; i++ {
		sum += int(b[i])
	}
	b[7] = byte(sum % 256)
	return b, hexUpper(b), nil
}

// Preset actions for GB28181 (byte 7 codes).
const (
	PresetSet    = "set"
	PresetGoto   = "goto"
	PresetDelete = "delete"
)

var presetCodes = map[string]byte{PresetSet: 0x81, PresetGoto: 0x82, PresetDelete: 0x83}

// BuildPresetCmd builds a GB28181 preset command (set/goto/delete) for index.
func BuildPresetCmd(action string, index int) ([]byte, string, error) {
	code, ok := presetCodes[strings.TrimSpace(action)]
	if !ok {
		return nil, "", fmt.Errorf("unsupported preset action %q", action)
	}
	if index < 1 || index > 255 {
		return nil, "", fmt.Errorf("preset index out of range (1-255)")
	}
	b := []byte{0xA5, 0x0F, 0x01, byte(index), 0, 0, code, 0}
	var sum int
	for i := 0; i < 7; i++ {
		sum += int(b[i])
	}
	b[7] = byte(sum % 256)
	return b, hexUpper(b), nil
}

// PanTiltZoom converts a command to ISAPI signed values (-100..100).
// pan: +right/-left, tilt: +up/-down, zoom: +in/-out.
func PanTiltZoom(cmd string, speed int) (pan, tilt, zoom int, err error) {
	cmd = strings.TrimSpace(cmd)
	parts, ok := combos[cmd]
	if !ok {
		return 0, 0, 0, fmt.Errorf("unsupported ptz cmd %q", cmd)
	}
	s := speed
	if s <= 0 {
		s = 50
	}
	if s > 100 {
		s = 100
	}
	for _, p := range parts {
		switch p {
		case CmdRight:
			pan = s
		case CmdLeft:
			pan = -s
		case CmdUp:
			tilt = s
		case CmdDown:
			tilt = -s
		case CmdZoomIn:
			zoom = s
		case CmdZoomOut:
			zoom = -s
		}
	}
	return pan, tilt, zoom, nil
}

func hexUpper(b []byte) string {
	const hexDigits = "0123456789ABCDEF"
	out := make([]byte, 0, len(b)*2)
	for _, v := range b {
		out = append(out, hexDigits[v>>4], hexDigits[v&0x0F])
	}
	return string(out)
}
