package server

import (
	"context"
	"errors"
	"strconv"

	"github.com/brutella/dnssd"
	"github.com/charmbracelet/log"
)

// ControlServiceType is the DNS-SD service type under which the control API
// is announced. Clients browse "_swkit._tcp.local." and read the API path
// from the "path" TXT key.
const ControlServiceType = "_swkit._tcp"

// controlAPIVersion is bumped when the control JSON API changes
// incompatibly; it is published in the TXT record and from <endpoint>/api/info.
const controlAPIVersion = 1

// controlServiceConfig builds the DNS-SD record for a control API served on
// port under endpoint. name becomes the instance name shown to users.
func controlServiceConfig(name string, port int, endpoint, version string) dnssd.Config {
	if name == "" {
		name = "swkit"
	}
	text := map[string]string{
		"path": endpoint,
		"api":  strconv.Itoa(controlAPIVersion),
	}
	if version != "" {
		text["ver"] = version
	}
	return dnssd.Config{
		Name: name,
		Type: ControlServiceType,
		Port: port,
		Text: text,
	}
}

// AdvertiseControl announces the control API over mDNS until ctx is
// cancelled. It runs its own responder next to HomeKit's; both share UDP
// 5353 the way independent mDNS responders on one host always do.
func AdvertiseControl(ctx context.Context, name string, port int, endpoint, version string, logger *log.Logger) error {
	if port <= 0 {
		return errors.New("control server port unknown, not advertising")
	}
	srv, err := dnssd.NewService(controlServiceConfig(name, port, endpoint, version))
	if err != nil {
		return errors.Join(err, errors.New("failed to build mDNS service"))
	}
	rp, err := dnssd.NewResponder()
	if err != nil {
		return errors.Join(err, errors.New("failed to create mDNS responder"))
	}
	if _, err := rp.Add(srv); err != nil {
		return errors.Join(err, errors.New("failed to add mDNS service"))
	}
	logger.Info("advertising control API over mDNS", "type", ControlServiceType, "name", srv.Name, "port", port, "path", endpoint)
	if err := rp.Respond(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return errors.Join(err, errors.New("mDNS responder stopped"))
	}
	return nil
}
