package shelly

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type requestSent struct {
	Id               uint   `json:"id"`
	Dst              string `json:"dst"`
	Method           string `json:"method"`
	sentAt           time.Time
	responseReceived bool
}

type requestQueue []requestSent

func (rq requestQueue) add(req requestFrame, dst string) requestQueue {
	added := append(rq, requestSent{
		Id:     req.Id,
		Dst:    dst,
		Method: req.Method,
		sentAt: time.Now(),
	})
	return added
}

func (rq requestQueue) getNextId() uint {
	id := uint(1)
	for _, req := range rq {
		if req.Id >= id {
			id = req.Id + 1
		}
	}
	return id
}

func (rq requestQueue) getRequest(id uint) *requestSent {
	for _, req := range rq {
		if req.Id == id {
			return &req
		}
	}
	return nil
}

type requestFrame struct {
	Jsonrpc string                 `json:"jsonrpc"`
	Id      uint                   `json:"id"`
	Src     string                 `json:"src"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

func (rf requestFrame) bytes() ([]byte, error) {
	return json.Marshal(rf)
}

type notificationFrame struct {
	Src    string          `json:"src"`
	Dst    string          `json:"dst"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (nf *notificationFrame) UnmarshalParams(params interface{}) error {
	if len(nf.Params) == 0 {
		return errors.New("failed to read json params: message does not contain any")
	}

	return json.Unmarshal(nf.Params, params)
}

type responseFrame struct {
	Id       uint            `json:"id"`
	Src      string          `json:"src"`
	Dst      string          `json:"dst"`
	Result   json.RawMessage `json:"result,omitempty"`
	ResError *rpcError       `json:"error,omitempty"`
}

func (rf *responseFrame) Error() error {
	if rf.ResError == nil {
		return nil
	}

	return fmt.Errorf("rpc response error code %d, msg %s", rf.ResError.Code, rf.ResError.Message)
}

func (rf *responseFrame) UnmarshalResult(result interface{}) error {
	if len(rf.Result) == 0 {
		return errors.New("failed to read json result: message does not contain any")
	}

	if rf.ResError != nil {
		return rf.Error()
	}

	return json.Unmarshal(rf.Result, result)
}

func unmarshalRpcResponse(data []byte) (rf *responseFrame, err error) {
	rf = &responseFrame{}
	err = json.Unmarshal(data, rf)
	return
}

func unmarshalRpcNotification(data []byte) (nf *notificationFrame, err error) {
	nf = &notificationFrame{}
	err = json.Unmarshal(data, nf)
	return
}

func unmarshalOnlineStatus(data []byte) (isOnline bool, err error) {
	err = json.Unmarshal(data, &isOnline)
	return
}
