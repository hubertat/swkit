package shelly

import (
	"encoding/json"
	"errors"
)

type rpcRequest struct {
	Jsonrpc string                 `json:"jsonrpc"`
	Src     string                 `json:"src"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
	Id      uint                   `json:"id"`
}

func (rr *rpcRequest) Bytes() ([]byte, error) {
	return json.Marshal(rr)
}

type RpcMessage struct {
	Id     *int            `json:"id,omitempty"`
	Src    string          `json:"src"`
	Dst    string          `json:"dst"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (rm *RpcMessage) UnmarshalParams(params interface{}) (err error) {
	if len(rm.Params) == 0 {
		err = errors.New("failed to read json params: message does not contain any")
		return
	}

	err = json.Unmarshal(rm.Params, params)
	return
}

func (rm *RpcMessage) UnmarshalResult(result interface{}) (err error) {
	if len(rm.Result) == 0 {
		err = errors.New("failed to read json result: message does not contain any")
		return
	}

	err = json.Unmarshal(rm.Result, result)
	return
}
