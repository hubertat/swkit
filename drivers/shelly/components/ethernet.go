package components

var IpV4ModesAvailable = []string{"dhcp", "static"}

type Ethernet struct {
	Config EthernetConfig
	Status EthernetStatus
}

type EthernetConfig struct {
	Enable   bool   `json:"enable"`
	IPv4Mode string `json:"ipv4mode"`

	IP         *string `json:"ip,omitempty"`
	Netmask    *string `json:"netmask,omitempty"`
	Gateway    *string `json:"gw,omitempty"`
	NameServer *string `json:"nameserver,omitempty"`
}

type EthernetStatus struct {
	Ip *string `json:"ip,omitempty"`
}
