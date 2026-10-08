package onvif

import "encoding/xml"

type deviceInformationResponse struct {
	Manufacturer string `xml:"Body>GetDeviceInformationResponse>Manufacturer"`
	Model        string `xml:"Body>GetDeviceInformationResponse>Model"`
	Firmware     string `xml:"Body>GetDeviceInformationResponse>FirmwareVersion"`
	Serial       string `xml:"Body>GetDeviceInformationResponse>SerialNumber"`
	Hardware     string `xml:"Body>GetDeviceInformationResponse>HardwareId"`
}

type capabilitiesResponse struct {
	MediaXAddr string `xml:"Body>GetCapabilitiesResponse>Capabilities>Media>XAddr"`
}

type profileXML struct {
	Token string `xml:"token,attr"`
	Name  string `xml:"Name"`
}

type profilesResponse struct {
	Profiles []profileXML `xml:"Body>GetProfilesResponse>Profiles"`
}

type streamURIResponse struct {
	URI string `xml:"Body>GetStreamUriResponse>MediaUri>Uri"`
}

type soapFault struct {
	Reason string `xml:"Body>Fault>Reason>Text"`
	Code   string `xml:"Body>Fault>Code>Value"`
}

// parseFault returns a human message when the reply is a SOAP Fault.
func parseFault(data []byte) string {
	var f soapFault
	if err := xml.Unmarshal(data, &f); err != nil {
		return ""
	}
	if f.Reason != "" {
		return "设备返回错误: " + f.Reason
	}
	if f.Code != "" {
		return "设备返回错误: " + f.Code
	}
	return ""
}
