package shelly

import (
	"encoding/json"
	"errors"

	"github.com/hubertat/swkit/drivers/shelly/components"
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
	Method string `json:"method"`
	Params struct {
		Switch0 *components.SwitchStatus `json:"switch:0,omitempty"`
		Switch1 *components.SwitchStatus `json:"switch:1,omitempty"`
		Switch2 *components.SwitchStatus `json:"switch:2,omitempty"`
		Switch3 *components.SwitchStatus `json:"switch:3,omitempty"`
	}
}

func (rssr *ShellyNotifyStatusResponse) SwitchSlice() []*components.SwitchStatus {
	ss := []*components.SwitchStatus{}
	if rssr.Params.Switch0 != nil {
		ss = append(ss, rssr.Params.Switch0)
	}

	if rssr.Params.Switch1 != nil {
		ss = append(ss, rssr.Params.Switch1)
	}
	if rssr.Params.Switch2 != nil {
		ss = append(ss, rssr.Params.Switch2)
	}
	if rssr.Params.Switch3 != nil {
		ss = append(ss, rssr.Params.Switch3)
	}

	return ss
}
