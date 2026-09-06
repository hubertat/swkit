package shelly

import (
	"errors"

	"github.com/hubertat/swkit/drivers/shelly/components"
	"github.com/hubertat/swkit/drivers/shelly/events"
)

type RawEvents struct {
	Events []shellyEvent `json:"events"`
	Ts     float64       `json:"ts"`
}

type shellyEvent struct {
	Ts        float64 `json:"ts"`
	Component string  `json:"component"`
	Id        uint    `json:"id"`
	Event     string  `json:"event"`
}

type Event struct {
	Id            uint
	ComponentType components.ComponentType
	ComponentId   uint
	EventType     events.ShellyEventType
}

func (re RawEvents) GetEvents() ([]Event, error) {
	evs := make([]Event, len(re.Events))
	for i, rawEvent := range re.Events {
		comp, err := components.ParseShellyComponentString(rawEvent.Component)
		if err != nil {
			return nil, errors.Join(err, errors.New("failed to parse component string"))
		}
		e := events.ParseShellyEventType(rawEvent.Event)
		if e == events.ShellyEventUndefined {
			return nil, errors.Join(err, errors.New("event type is undefined"))
		}
		evs[i] = Event{
			Id:            rawEvent.Id,
			ComponentType: comp.Type,
			ComponentId:   comp.Id,
			EventType:     e,
		}
	}
	return evs, nil
}
