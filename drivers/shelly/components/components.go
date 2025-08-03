package components

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type ComponentType uint

const (
	ComponentTypeUndefined ComponentType = iota
	ComponentTypeSwitch
	ComponentTypeInput
)

func AllComponentTypes() []ComponentType {
	return []ComponentType{ComponentTypeSwitch, ComponentTypeInput}
}
func (sct ComponentType) String() string {
	switch sct {
	case ComponentTypeSwitch:
		return "switch"
	case ComponentTypeInput:
		return "input"
	default:
		return ""
	}
}

type ShellyComponent struct {
	Id   uint
	Type ComponentType
}

func ParseShellyComponentString(componentString string) (ShellyComponent, error) {
	sc := ShellyComponent{}

	if len(componentString) == 0 {
		return sc, errors.New("empty component string")
	}

	parts := strings.Split(componentString, ":")
	if len(parts) > 2 || len(parts) == 0 {
		return sc, fmt.Errorf("unexpected component string format: %s", componentString)
	}

	if len(parts) == 2 {
		id, convErr := strconv.Atoi(parts[1])
		if convErr == nil {
			sc.Id = uint(id)
		}
	}

	if len(parts[0]) == 0 {
		return sc, fmt.Errorf("unexpected component string format: %s", componentString)
	}

	for _, ct := range AllComponentTypes() {
		if strings.EqualFold(ct.String(), parts[0]) {
			sc.Type = ct
			return sc, nil
		}
	}

	return sc, fmt.Errorf("failed processing component string, type not recognized: %s", componentString)
}
