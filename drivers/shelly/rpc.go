package shelly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const wsConnectionTimeout = 5 * time.Second

type RpcClient struct {
	rpcSrc   string
	wsConn   *websocket.Conn
	requests []rpcRequest
	mutex    sync.Mutex
}

func (rc *RpcClient) newRpcRequest(method string, params map[string]interface{}) rpcRequest {
	rc.mutex.Lock()
	defer rc.mutex.Unlock()

	nextId := uint(len(rc.requests))
	req := rpcRequest{
		Jsonrpc: "2.0",
		Src:     rc.rpcSrc,
		Method:  method,
		Params:  params,
		Id:      nextId,
	}
	rc.requests = append(rc.requests, req)
	// log.Println("---- prepared msg with src: ", rc.rpcSrc, " and id: ", nextId)
	return req
}

func (rc *RpcClient) SendJson(method string, params map[string]interface{}) error {
	req := rc.newRpcRequest(method, params)
	return rc.wsConn.WriteJSON(req)
}

func (rc *RpcClient) SendJsonAwait(ctx context.Context, method string, params map[string]interface{}) (RpcMessage, error) {
	req := rc.newRpcRequest(method, params)

	msgChan := make(chan RpcMessage)
	errChan := make(chan error)

	go func() {
		msg, err := rc.ReadJsonMessage()
		if err != nil {
			errChan <- errors.Join(errors.New("failed to read json rpc message"), err)
			return
		}
		msgChan <- msg
	}()

	err := rc.wsConn.WriteJSON(req)
	if err != nil {
		return RpcMessage{}, errors.Join(errors.New("failed to write json rpc message"), err)
	}

	for {
		select {
		case <-ctx.Done():
			err = ctx.Err()
			if err != nil {
				return RpcMessage{}, errors.Join(errors.New("context error: "), err)
			}
		case err := <-errChan:
			return RpcMessage{}, err
		case msg := <-msgChan:
			if msg.Id == nil {
				log.Println("[rpc] nil id message")
			} else {
				if uint(*msg.Id) == req.Id {
					return msg, nil
				} else {
					log.Println("[rpc] not matching id message")
				}
			}
		}
	}

}

func (rc *RpcClient) checkAndRemoveRequest(id uint) (rpcRequest, error) {
	found := false
	var request rpcRequest
	for i, req := range rc.requests {
		if req.Id == id {
			found = true
			req.answered = true
			rc.requests[i] = req
			request = req
			break
		}
	}
	if !found {
		return rpcRequest{}, errors.New("request not found")
	}

	return request, nil
}

func (rc *RpcClient) ReadJsonMessage() (message RpcMessage, err error) {
	err = rc.wsConn.ReadJSON(&message)
	if err != nil {
		err = errors.Join(errors.New("failed to read json rpc message"), err)
	}
	if !strings.EqualFold(message.Dst, rc.rpcSrc) {
		err = fmt.Errorf("message destination does not match, got: %s, want: %s", message.Dst, rc.rpcSrc)
		return
	}
	if message.Id == nil {
		return
	}
	_, err = rc.checkAndRemoveRequest(uint(*message.Id))
	if err != nil {
		err = errors.Join(errors.New("failed to find matching request"), err)
		return
	}
	// apparently the shelly devices do not send the method in the response, only for notifications
	// if !strings.EqualFold(message.Method, req.Method) {
	// 	err = fmt.Errorf("message method [%s] does not match request method [%s]", message.Method, req.Method)
	// }

	return
}

func (rc *RpcClient) Close() error {
	return rc.wsConn.Close()
}

func NewRpcClient(ctx context.Context, originUrl *url.URL, targetUrl *url.URL) (*RpcClient, error) {

	dialer := websocket.Dialer{
		HandshakeTimeout: wsConnectionTimeout,
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
	}
	targetUrl.Scheme = "ws"
	originUrl.Scheme = "ws"
	headers := http.Header{}
	headers.Add("Origin", originUrl.String())
	targetUrl = targetUrl.JoinPath("rpc")

	wsConn, _, err := dialer.DialContext(ctx, targetUrl.String(), headers)
	if err != nil {
		return nil, errors.Join(errors.New("failed to ws dial"), err)
	}

	return &RpcClient{
		mutex:  sync.Mutex{},
		wsConn: wsConn,
		rpcSrc: "swkitRpcCli",
	}, nil
}

type rpcRequest struct {
	Jsonrpc string                 `json:"jsonrpc"`
	Src     string                 `json:"src"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
	Id      uint                   `json:"id"`

	answered bool
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
