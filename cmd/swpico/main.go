package main

import (
	"fmt"
	"machine"
	"time"

	"github.com/hubertat/swkit/swpico"
)

func main() {
	pTypeOne := swpico.PicoType1()
	err := pTypeOne.Setup()
	if err != nil {
		fmt.Println("setup failed: ", err.Error())
		panic(err)
	}

	fmt.Println("setup OK!")

	led := machine.LED
	led.Configure(machine.PinConfig{Mode: machine.PinOutput})

	for true {
		led.Low()
		time.Sleep(time.Millisecond * 700)

		led.High()
		time.Sleep(time.Millisecond * 100)
		led.Low()
		time.Sleep(time.Millisecond * 300)
		led.High()
		time.Sleep(time.Millisecond * 100)
		led.Low()
	}
}
