package components

type DeviceInfo struct {
	ID           string  `json:"id"`
	MAC          string  `json:"mac"`
	Model        string  `json:"model"`
	Gen          int     `json:"gen"`
	FirmwareID   string  `json:"fw_id"`
	Version      string  `json:"ver"`
	App          string  `json:"app"`
	Profile      *string `json:"profile,omitempty"`
	AuthEnabled  bool    `json:"auth_en"`
	AuthDomain   *string `json:"auth_domain,omitempty"`
	Discoverable bool    `json:"discoverable"`
}
