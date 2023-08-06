package components

type Wifi struct {
	Config WifiConfig
	Status WifiStatus
}

type WifiConfig struct {
	AP   APConfig   `json:"ap"`
	Sta  StaConfig  `json:"sta"`
	Sta1 *StaConfig `json:"sta1,omitempty"`
	Roam RoamConfig `json:"roam"`
}

type APConfig struct {
	SSID          string       `json:"ssid"`
	Pass          *string      `json:"pass,omitempty"`
	IsOpen        bool         `json:"is_open"`
	Enable        bool         `json:"enable"`
	RangeExtender *ExtenderCfg `json:"range_extender,omitempty"`
}

type StaConfig struct {
	SSID   string  `json:"ssid"`
	Pass   *string `json:"pass,omitempty"`
	IsOpen bool    `json:"is_open"`
	Enable bool    `json:"enable"`

	IPv4Mode string `json:"ipv4mode"`

	IP         *string `json:"ip,omitempty"`
	Netmask    *string `json:"netmask,omitempty"`
	Gateway    *string `json:"gw,omitempty"`
	NameServer *string `json:"nameserver,omitempty"`
}

type ExtenderCfg struct {
	Enable bool `json:"enable"`
}

type RoamConfig struct {
	RSSIThreshold int `json:"rssi_thr"`
	Interval      int `json:"interval"`
}

type WifiStatus struct {
	StaIP         *string `json:"sta_ip,omitempty"`
	Status        string  `json:"status"`
	SSID          *string `json:"ssid,omitempty"`
	RSSI          int     `json:"rssi"`
	APClientCount *int    `json:"ap_client_count,omitempty"`
}
