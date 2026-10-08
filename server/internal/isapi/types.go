package isapi

// deviceInfoDoc maps /ISAPI/System/deviceInfo.
type deviceInfoDoc struct {
	Name     string `xml:"deviceName"`
	DeviceID string `xml:"deviceID"`
	Model    string `xml:"model"`
	Serial   string `xml:"serialNumber"`
	Firmware string `xml:"firmwareVersion"`
	MAC      string `xml:"macAddress"`
}

// streamingChannelList maps /ISAPI/Streaming/channels.
type streamingChannelList struct {
	Channels []streamingChannel `xml:"StreamingChannel"`
}

type streamingChannel struct {
	ID        string `xml:"id"`
	Name      string `xml:"channelName"`
	Enabled   string `xml:"enabled"`
	Transport struct {
		RTSPPort int `xml:"rtspPortNo"`
	} `xml:"Transport"`
}
