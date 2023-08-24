package shelly

import (
	"encoding/json"
	"errors"
)

type RpcClient struct {
	rpcSrc   string
	requests []RpcRequest
}

func (rc *RpcClient) NewRpcRequest(method string, params map[string]interface{}) RpcRequest {
	req := RpcRequest{
		Jsonrpc: "2.0",
		Src:     rc.rpcSrc,
		Method:  method,
		Params:  params,
		Id:      len(rc.requests) + 1,
	}

	rc.requests = append(rc.requests, req)
	return req
}

func (rc *RpcClient) GetRequest(id int) (RpcRequest, error) {
	if len(rc.requests) < id {
		return RpcRequest{}, errors.New("Request not found")
	}

	return rc.requests[id-1], nil
}

func NewRpcClient(RpcSrc string) *RpcClient {
	return &RpcClient{
		rpcSrc: RpcSrc,
	}
}

type RpcRequest struct {
	Jsonrpc string                 `json:"jsonrpc"`
	Src     string                 `json:"src"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
	Id      int                    `json:"id"`
}

type RpcResponse struct {
	Id     int         `json:"id"`
	Src    string      `json:"src"`
	Dst    string      `json:"dst"`
	Result interface{} `json:"result"`
}

func (rres *RpcResponse) UnmarshalResult(v interface{}) error {
	bytes, err := json.Marshal(rres.Result)
	if err != nil {
		return errors.Join(errors.New("failed to marshal result into bytes"), err)
	}

	err = json.Unmarshal(bytes, v)
	if err != nil {
		return errors.Join(errors.New("failed to unmarshal bytes into result"), err)
	}
	return nil
}

type ShellyNotifyStatusResponse struct {
	Id     int    `json:"id"`
	Src    string `json:"src"`
	Dst    string `json:"dst"`
	Result struct {
		Switch0 json.RawMessage `json:"switch:0"`
		Switch1 json.RawMessage `json:"switch:1"`
		Switch2 json.RawMessage `json:"switch:2"`
		Switch3 json.RawMessage `json:"switch:3"`
		Input0  json.RawMessage `json:"input:0"`
		Input1  json.RawMessage `json:"input:1"`
		Input2  json.RawMessage `json:"input:2"`
		Input3  json.RawMessage `json:"input:3"`
	} `json:"result"`
}

func (rssr *ShellyNotifyStatusResponse) SwitchSlice() [][]byte {
	return [][]byte{rssr.Result.Switch0, rssr.Result.Switch1, rssr.Result.Switch2, rssr.Result.Switch3}
}

func (rssr *ShellyNotifyStatusResponse) InputSlice() [][]byte {
	return [][]byte{rssr.Result.Input0, rssr.Result.Input1, rssr.Result.Input2, rssr.Result.Input3}
}

// func (rssr *ShellyNotifyStatusResponse) SwitchSlice() []*components.SwitchStatus {
// 	ss := []*components.SwitchStatus{}
// 	if rssr.Result.Switch0 != nil {
// 		ss = append(ss, rssr.Result.Switch0)
// 	}
// 	if rssr.Result.Switch1 != nil {
// 		ss = append(ss, rssr.Result.Switch1)
// 	}
// 	if rssr.Result.Switch2 != nil {
// 		ss = append(ss, rssr.Result.Switch2)
// 	}
// 	if rssr.Result.Switch3 != nil {
// 		ss = append(ss, rssr.Result.Switch3)
// 	}

// 	return ss
// }

// func (rssr *ShellyNotifyStatusResponse) InputSlice() []*components.InputStatus {
// 	is := []*components.InputStatus{}
// 	if rssr.Result.Input0 != nil {
// 		is = append(is, rssr.Result.Input0)
// 	}
// 	if rssr.Result.Input1 != nil {
// 		is = append(is, rssr.Result.Input1)
// 	}
// 	if rssr.Result.Input2 != nil {
// 		is = append(is, rssr.Result.Input2)
// 	}
// 	if rssr.Result.Input3 != nil {
// 		is = append(is, rssr.Result.Input3)
// 	}

// 	return is
// }
