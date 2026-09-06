package shelly

import "encoding/json"

// AnnouncePayload holds the fields from a Shelly device's MQTT announce message.
type AnnouncePayload struct {
	Id    string `json:"id"`
	Model string `json:"model"`
	Mac   string `json:"mac"`
	App   string `json:"app"`
	Ver   string `json:"ver"`
	Gen   int    `json:"gen"`
}

// ParseAnnounce decodes an MQTT announce payload into an AnnouncePayload.
func ParseAnnounce(payload []byte) (*AnnouncePayload, error) {
	var ap AnnouncePayload
	if err := json.Unmarshal(payload, &ap); err != nil {
		return nil, err
	}
	return &ap, nil
}
