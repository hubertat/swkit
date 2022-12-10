package drivers

type SensorDriver interface {
	Setup([]TemperatureSensor) error
	Close() error
	IsReady() bool
	Name() string
	Sync() error
	FindTemperatureSensor(string) (TemperatureSensor, error)
}

type TemperatureSensor interface {
	GetValue() (float64, error)
	SetValue(float64) error
	GetTags() map[string]string
	GetId() string
}
