package mqtt

import (
	"sync"
	"time"
)

type requestSent struct {
	Id     uint   `json:"id"`
	Dst    string `json:"dst"`
	Method string `json:"method"`
	sentAt time.Time
}

type requestQueue struct {
	queue []requestSent

	idLock sync.Mutex
}

func newRequestQueue() *requestQueue {
	return &requestQueue{
		queue: []requestSent{},
	}
}

func (rq *requestQueue) add(req requestFrame, dst string) {
	rq.idLock.Lock()
	defer rq.idLock.Unlock()

	rq.queue = append(rq.queue, requestSent{
		Id:     req.Id,
		Dst:    dst,
		Method: req.Method,
		sentAt: time.Now(),
	})
}

func (rq *requestQueue) getNextId() uint {
	rq.idLock.Lock()
	defer rq.idLock.Unlock()

	id := uint(1)
	for _, req := range rq.queue {
		if req.Id >= id {
			id = req.Id + 1
		}
	}
	return id
}

func (rq *requestQueue) getRequest(id uint) *requestSent {
	for _, req := range rq.queue {
		if req.Id == id {
			return &req
		}
	}
	return nil
}
