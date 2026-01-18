package wago

import "errors"

const defaultModbusPort = 502

// Wago 750-362 modbus tcp controller
type Wago362 struct {
	address string
	port    uint
	modules []WagoModule
}

type WagoModule interface {
	String() string
	DiCount() int
	DoCount() int
}

type WagoControllerConfig struct {
	Address string
	Port    uint
	Modules []string
}

func (wcc WagoControllerConfig) Sanitize() error {
	if len(wcc.Address) < 6 {
		return errors.New("invalid address")
	}

	if wcc.Port == 0 {
		wcc.Port = defaultModbusPort
	}

	return nil
}

func NewWago362(conf WagoControllerConfig) (Wago362, error) {
	err := conf.Sanitize()
	if err != nil {
		return Wago362{}, errors.Join(err, errors.New("failed to sanitize configuration"))
	}

	// TODO try to connect to controller (check if reachable)

	modules := []WagoModule{}
	for _, modulePartNo := range conf.Modules {
		m, err := findWagoModule(modulePartNo)
		if err != nil {
			return Wago362{}, errors.Join(err, errors.New("failed to find module"))
		}
		modules = append(modules, m)
	}

	return Wago362{
		address: conf.Address,
		port:    conf.Port,
	}, nil
}

func findWagoModule(partNo string) (WagoModule, error) {
	switch partNo {
	case "750-512":
		return &wago512{}, nil
	case "750-611":
		return &wago611{}, nil
	case "750-436":
		return &wago436{}, nil
	default:
		return nil, errors.New("unknown module: " + partNo)
	}
}

func listWagoModules() []WagoModule {
	return []WagoModule{
		&wago512{},
		&wago611{},
		&wago436{},
	}
}
