package swpico

type Action int

const (
	ActionSwitchOn Action = iota
	ActionSwitchOff
	ActionToggle
)

type Event struct {
	actionMap map[Action]*Output
}

func NewEvent(action Action, out *Output) Event {
	return Event{
		actionMap: map[Action]*Output{
			action: out,
		},
	}
}

func (e *Event) Fire() error {
	for action, output := range e.actionMap {
		switch action {
		case ActionSwitchOn:
			output.SwitchOn()
		case ActionSwitchOff:
			output.SwitchOff()
		case ActionToggle:
			output.Toggle()
		}
	}

	return nil
}
