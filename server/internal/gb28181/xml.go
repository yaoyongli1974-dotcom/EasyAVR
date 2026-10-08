package gb28181

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// Keepalive is a device heartbeat (CmdType=Keepalive).
type Keepalive struct {
	CmdType  string `xml:"CmdType"`
	SN       int    `xml:"SN"`
	DeviceID string `xml:"DeviceID"`
	Status   string `xml:"Status"`
}

// CatalogResponse is a device's reply to a Catalog query.
type CatalogResponse struct {
	CmdType    string      `xml:"CmdType"`
	SN         int         `xml:"SN"`
	DeviceID   string      `xml:"DeviceID"`
	SumNum     int         `xml:"SumNum"`
	DeviceList CatalogList `xml:"DeviceList"`
}

// CatalogList holds catalog items.
type CatalogList struct {
	Num   int           `xml:"Num,attr"`
	Items []CatalogItem `xml:"Item"`
}

// CatalogItem is a single channel advertised by a device.
type CatalogItem struct {
	DeviceID     string `xml:"DeviceID"`
	Name         string `xml:"Name"`
	Manufacturer string `xml:"Manufacturer"`
	Model        string `xml:"Model"`
	Owner        string `xml:"Owner"`
	CivilCode    string `xml:"CivilCode"`
	Address      string `xml:"Address"`
	ParentID     string `xml:"ParentID"`
	Parental     int    `xml:"Parental"`
	RegisterWay  int    `xml:"RegisterWay"`
	Status       string `xml:"Status"`
}

// DeviceInfoResponse carries device metadata.
type DeviceInfoResponse struct {
	CmdType      string `xml:"CmdType"`
	SN           int    `xml:"SN"`
	DeviceID     string `xml:"DeviceID"`
	Name         string `xml:"DeviceName"`
	Manufacturer string `xml:"Manufacturer"`
	Model        string `xml:"Model"`
	Firmware     string `xml:"Firmware"`
	Channel      int    `xml:"Channel"`
}

// Alarm is a device alarm notification.
type Alarm struct {
	CmdType          string `xml:"CmdType"`
	SN               int    `xml:"SN"`
	DeviceID         string `xml:"DeviceID"`
	AlarmPriority    string `xml:"AlarmPriority"`
	AlarmMethod      string `xml:"AlarmMethod"`
	AlarmTime        string `xml:"AlarmTime"`
	AlarmDescription string `xml:"AlarmDescription"`
	Longitude        string `xml:"Longitude"`
	Latitude         string `xml:"Latitude"`
}

// envelope peeks only CmdType from an arbitrary MANSCDP body.
type envelope struct {
	CmdType string `xml:"CmdType"`
}

// cmdType extracts the CmdType of a body.
func cmdType(body string) string {
	var e envelope
	_ = xml.Unmarshal([]byte(body), &e)
	return e.CmdType
}

// xmlUnmarshal is a small helper wrapping xml.Unmarshal.
func xmlUnmarshal(body string, v any) error {
	return xml.Unmarshal([]byte(body), v)
}

// decodeBody converts a possibly GB2312/GBK encoded XML body to UTF-8.
func decodeBody(raw []byte) string {
	head := strings.ToLower(string(raw))
	if strings.Contains(head, "gb2312") || strings.Contains(head, "gbk") || strings.Contains(head, "gb18030") {
		if out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw); err == nil {
			return string(out)
		}
	}
	return string(raw)
}

// marshalXML serializes a value to an XML body with the GB28181 declaration.
func marshalXML(v any) (string, error) {
	b, err := xml.Marshal(v)
	if err != nil {
		return "", err
	}
	return xml.Header + string(b), nil
}

// buildQuery constructs a MANSCDP Query body (Catalog / DeviceInfo / ...).
func buildQuery(cmdType, deviceID string, sn int) (string, error) {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	fmt.Fprintf(&b, "<Query><CmdType>%s</CmdType><SN>%d</SN><DeviceID>%s</DeviceID></Query>", cmdType, sn, deviceID)
	return b.String(), nil
}

// parseCatalog parses a Catalog response body.
func parseCatalog(body string) (*CatalogResponse, error) {
	var c CatalogResponse
	if err := xml.Unmarshal([]byte(body), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// parseDeviceInfo parses a DeviceInfo response body.
func parseDeviceInfo(body string) (*DeviceInfoResponse, error) {
	var d DeviceInfoResponse
	if err := xml.Unmarshal([]byte(body), &d); err != nil {
		return nil, err
	}
	return &d, nil
}
