package drivers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
)

const remoteIoSlaveDriverName = "remoteio_slave"
const pushButtonReleaseMs = 200
const httpTimeoutsMs = 3000

type RemoteIoSlave struct {
	Token    string
	HttpAddr string

	inputs  []*InFromRemoteIo
	outputs []*OutFromRemoteIo
	ready   bool
	server  *http.Server

	serverErr chan error
}

func (ris *RemoteIoSlave) NameId() string {
	return remoteIoSlaveDriverName
}

func (ris *RemoteIoSlave) IsReady() bool {
	return ris.ready
}

func (ris *RemoteIoSlave) Close() error {
	return ris.server.Close()
}

func (ris *RemoteIoSlave) Setup(ctx context.Context, inputs []uint16, outputs []uint16) error {

	for _, inPin := range inputs {
		ris.inputs = append(ris.inputs, &InFromRemoteIo{pin: uint8(inPin)})
	}

	for _, outPin := range outputs {
		ris.outputs = append(ris.outputs, &OutFromRemoteIo{pin: uint8(outPin)})
	}

	handler := httprouter.New()
	handler.GET("/push/:pin_no/event/:event/token/:token", ris.handlePush)

	httpTimeout := httpTimeoutsMs * time.Millisecond

	ris.server = &http.Server{
		Addr:              ris.HttpAddr,
		Handler:           handler,
		ReadTimeout:       httpTimeout,
		ReadHeaderTimeout: httpTimeout,
		WriteTimeout:      httpTimeout,
		IdleTimeout:       2 * httpTimeout,
	}

	ris.serverErr = make(chan error)

	ris.ready = true
	go func() {
		ris.serverErr <- ris.server.ListenAndServe()
		ris.ready = false
	}()

	return nil
}

func (ris *RemoteIoSlave) GetInput(pin uint16) (DigitalInput, error) {
	for _, in := range ris.inputs {
		if in.pin == uint8(pin) {
			return in, nil
		}
	}

	return nil, fmt.Errorf("remoteio slave input no: %d not found", pin)
}

func (ris *RemoteIoSlave) GetOutput(pin uint16) (DigitalOutput, error) {
	for _, out := range ris.outputs {
		if out.pin == uint8(pin) {
			return out, nil
		}
	}

	return nil, fmt.Errorf("remoteio slave output no: %d not found", pin)
}

func (ris *RemoteIoSlave) GetAllIo() (inputs []uint16, outputs []uint16) {
	for _, input := range ris.inputs {
		inputs = append(inputs, uint16(input.pin))
	}

	for _, output := range ris.outputs {
		outputs = append(outputs, uint16(output.pin))
	}

	return
}

func (ris *RemoteIoSlave) handlePush(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	if !strings.EqualFold(p.ByName("token"), ris.Token) {
		http.Error(w, "token mismatch", http.StatusUnauthorized)
		return
	}

	var input *InFromRemoteIo
	pinNo, _ := strconv.Atoi(p.ByName("pin_no"))

	for _, in := range ris.inputs {
		if in.pin == uint8(pinNo) {
			input = in
		}
	}

	if input == nil {
		http.Error(w, "pin not found", http.StatusNotFound)
		return
	}

	// log.Println("Debug: remoteioslave push match! event: ", p.ByName("event"))

	switch p.ByName("event") {
	case "single":
		input.listener.FireEvent(PushEventSinglePress)
	case "double":
		input.listener.FireEvent(PushEventDoublePress)
	case "long":
		input.listener.FireEvent(PushEventLongPress)
	default:
		http.Error(w, "unrecognized push event type", http.StatusInternalServerError)
	}

}

type OutFromRemoteIo struct {
	pin   uint8
	state bool
}

func (ofr *OutFromRemoteIo) GetState() (bool, error) {
	return ofr.state, nil
}

func (ofr *OutFromRemoteIo) Set(newState bool) error {
	ofr.state = newState
	return nil
}

type InFromRemoteIo struct {
	pin      uint8
	state    bool
	listener EventListener
}

func (ifr *InFromRemoteIo) GetState() (bool, error) {
	return ifr.state, nil
}

func (ifr *InFromRemoteIo) SubscribeToPushEvent(listener EventListener) error {
	ifr.listener = listener
	return nil
}
