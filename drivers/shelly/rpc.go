package shelly

import (
	"encoding/json"
	"errors"

	"github.com/gorilla/websocket"
)

type RpcClient struct {
	rpcSrc   string
	wsConn   *websocket.Conn
	requests []rpcRequest
}

func (rc *RpcClient) newRpcRequest(method string, params map[string]interface{}) rpcRequest {
	req := rpcRequest{
		Jsonrpc: "2.0",
		Src:     rc.rpcSrc,
		Method:  method,
		Params:  params,
		Id:      len(rc.requests) + 1,
	}

	rc.requests = append(rc.requests, req)
	return req
}

func (rc *RpcClient) SendJson(method string, params map[string]interface{}) error {
	req := rc.newRpcRequest(method, params)
	return rc.wsConn.WriteJSON(req)
}

func (rc *RpcClient) GetRequest(id int) (rpcRequest, error) {
	if len(rc.requests) < id {
		return rpcRequest{}, errors.New("request not found")
	}

	return rc.requests[id-1], nil
}

func (rc *RpcClient) ReadJsonMessage() (message RpcMessage, err error) {
	err = rc.wsConn.ReadJSON(&message)
	if err != nil {
		err = errors.Join(errors.New("failed to read json rpc message"), err)
	}
	return
}

func NewRpcClient(source string, wsConnection *websocket.Conn) *RpcClient {
	return &RpcClient{
		wsConn: wsConnection,
		rpcSrc: source,
	}
}

type rpcRequest struct {
	Jsonrpc string                 `json:"jsonrpc"`
	Src     string                 `json:"src"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
	Id      int                    `json:"id"`
}

type RpcMessage struct {
	Id     int             `json:"id"`
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
