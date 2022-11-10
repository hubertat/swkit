package swkit

import (
	"fmt"
	"time"

	"github.com/brutella/hc/accessory"
	"github.com/stianeikeland/go-rpio"
)

type Shutter struct {
	Name             string
	GpioOn           int
	GpioDirection    int
	GoUpByGpio       int
	GoDownByGpio     int
	MovementDuration string

	InvertOn        bool
	InvertDirection bool
	State           int

	hk                   *WindowShutter
	isMoving             bool
	isGoingUp            bool
	movementStartedAt    time.Time
	movementStartState   int
	fullMovementDuration time.Duration
	targetState          int
}

func (shu *Shutter) GetHk() *accessory.Accessory {
	info := accessory.Info{
		Name:         shu.Name,
		ID:           uint64(shu.GpioOn),
		SerialNumber: fmt.Sprintf("light:gpio:%02d/%02d", shu.GpioOn, shu.GpioDirection),
	}
	shu.hk = NewWindowShutter(info)

	shu.hk.WindowCovering.TargetPosition.OnValueRemoteUpdate(shu.StartMovement)
	shu.hk.WindowCovering.CurrentPosition.OnValueRemoteGet(shu.GetState)
	shu.hk.WindowCovering.PositionState.OnValueRemoteGet(shu.GetPositionState)

	return shu.hk.Accessory
}

func (shu *Shutter) GetState() int {
	return shu.State
}

func (shu *Shutter) GetPositionState() int {
	if !shu.isMoving {
		return 2
	}
	if shu.isGoingUp {
		return 1
	}
	return 0
}
func (shu *Shutter) GoUp() {
	shu.StartMovement(100)
}

func (shu *Shutter) GoDown() {
	shu.StartMovement(0)
}

func (shu *Shutter) StartMovement(target int) {
	fmt.Printf("DEBUG== StartMovement target: %v\n", target)
	shu.targetState = target
	if shu.State == target {
		shu.StopMovement()
		return
	}

	shu.movementStartedAt = time.Now()
	shu.movementStartState = shu.State
	shu.isGoingUp = target > shu.State
	fmt.Println("DEBUG ------> Movement!")
	shu.isMoving = true
}

func (shu *Shutter) StopMovement() {
	fmt.Println("DEBUG ++ StopMoevement")
	shu.isMoving = false
	shu.movementStartedAt = time.Time{}
	shu.hk.WindowCovering.CurrentPosition.SetValue(shu.State)
}

func (shu *Shutter) updateCurrentState() {
	if !shu.isMoving {
		return
	}
	percentageMoved := float64(100*time.Since(shu.movementStartedAt)) / float64(shu.fullMovementDuration)
	calculatedState := float64(shu.movementStartState)
	if shu.isGoingUp {
		calculatedState += percentageMoved
	} else {
		calculatedState -= percentageMoved
	}
	fmt.Printf("t=DEBUG percentage: %f, calc: %f\n", percentageMoved, calculatedState)
	if calculatedState >= 100 {
		shu.State = 100
		shu.StopMovement()
		return
	}
	if calculatedState <= 0 {
		shu.State = 0
		shu.StopMovement()
		return
	}

	shu.State = int(calculatedState)
	if shu.isGoingUp && shu.State >= shu.targetState {
		shu.StopMovement()
		return
	}
	if !shu.isGoingUp && shu.State <= shu.targetState {
		shu.StopMovement()
		return
	}
	shu.hk.WindowCovering.CurrentPosition.SetValue(shu.State)
}

func (shu *Shutter) SetupGpio() {
	pinOn := rpio.Pin(shu.GpioOn)
	pinDir := rpio.Pin(shu.GpioDirection)
	pinOn.Output()
	pinDir.Output()

	duration, err := time.ParseDuration(shu.MovementDuration)
	if err != nil {
		duration = 30 * time.Second
	}
	shu.fullMovementDuration = duration
}

func (shu *Shutter) Sync() {
	shu.updateCurrentState()
	fmt.Printf("DEBUG shutter move? %v position: %03d\n", shu.isMoving, shu.State)
	pinOn := rpio.Pin(shu.GpioOn)
	pinDir := rpio.Pin(shu.GpioDirection)

	goingUp := shu.isGoingUp
	if shu.InvertDirection {
		goingUp = !goingUp
	}
	if shu.isMoving {
		if goingUp {
			pinDir.High()
		} else {
			pinDir.Low()
		}
		if shu.InvertOn {
			pinOn.Low()
		} else {
			pinOn.High()
		}
	} else {
		if shu.InvertOn {
			pinOn.High()
		} else {
			pinOn.Low()
		}
	}
}
