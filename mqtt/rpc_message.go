package mqtt

import (
	"encoding/json"
	"errors"
	"fmt"
)

// RpcRequest represents a json rpc request with fields required from a client
type RpcRequest struct {
	Dst    string
	Method string
	Params map[string]interface{}
}

// RpcMessage represents a json rpc message - request, response or notification
// It is used to unmarshal the message and get the params, result or error
type RpcMessage struct {
	Src     string
	Dst     string
	Method  string
	MsgType RpcMessageType
	params  json.RawMessage
	result  json.RawMessage
	err     *rpcError
}

// UnmarshalResult unmarshals the result of the rpc message into variable v, which should be a pointer to a struct
func (rm *RpcMessage) UnmarshalResult(v interface{}) error {
	if rm.result == nil {
		return errors.New("result not present")
	}
	return json.Unmarshal(rm.result, v)
}

// UnmarshalParams unmarshals the params of the rpc message into variable v, which should be a pointer to a struct
func (rm *RpcMessage) UnmarshalParams(v interface{}) error {
	if rm.params == nil {
		return errors.New("params not present")
	}
	return json.Unmarshal(rm.params, v)
}

// Error returns error of the rpc message if present
func (rm *RpcMessage) Error() error {
	if rm.err != nil {
		return fmt.Errorf("rpc error code: %d, msg: %s", rm.err.Code, rm.err.Message)
	}
	return nil
}

// ErrorCode returns the error code of the rpc message
func (rm *RpcMessage) ErrorCode() int {
	if rm.err == nil {
		return 0
	}
	return rm.err.Code
}
