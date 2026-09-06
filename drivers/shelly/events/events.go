package events

import "strings"

/*
Shelly input have following event types:
btn_down
btn_up
single_push
double_push
triple_push
long_push
*/
type ShellyEventType uint

const (
	ShellyEventUndefined = ShellyEventType(iota)
	ShellyEventBtnDown
	ShellyEventBtnUp
	ShellyEventSinglePush
	ShellyEventDoublePush
	ShellyEventTriplePush
	ShellyEventLongPush
)

func AllShellyEventTypes() []ShellyEventType {
	return []ShellyEventType{
		ShellyEventBtnDown,
		ShellyEventBtnUp,
		ShellyEventSinglePush,
		ShellyEventDoublePush,
		ShellyEventTriplePush,
		ShellyEventLongPush,
	}
}

func (iet ShellyEventType) String() string {
	switch iet {
	case ShellyEventBtnDown:
		return "btn_down"
	case ShellyEventBtnUp:
		return "btn_up"
	case ShellyEventSinglePush:
		return "single_push"
	case ShellyEventDoublePush:
		return "double_push"
	case ShellyEventTriplePush:
		return "triple_push"
	case ShellyEventLongPush:
		return "long_push"
	default:
		return "undefined"
	}
}

func ParseShellyEventType(eventString string) ShellyEventType {
	for _, eventType := range AllShellyEventTypes() {
		if strings.EqualFold(eventString, eventType.String()) {
			return eventType
		}
	}
	return ShellyEventUndefined
}
