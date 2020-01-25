package buff

import (
	"sync"
)

type (
	Buff struct {
		mu   sync.Mutex
		buff []string
	}
)

func NewBuff() *Buff {
	return &Buff{
		buff: make([]string, 0),
	}
}

func (this *Buff) Enqueue(text string) {
	this.mu.Lock()
	defer this.mu.Unlock()

	this.buff = append(this.buff, text)
}

func (this *Buff) Dequeue() string {
	this.mu.Lock()
	defer this.mu.Unlock()

	var r string
	if len(this.buff) == 1 {
		r, this.buff = this.buff[0], make([]string, 0)
	} else {
		r, this.buff = this.buff[0], this.buff[1:]
	}
	return r
}

func (this *Buff) BulkDequeue(cnt int) []string {
	this.mu.Lock()
	defer this.mu.Unlock()

	r := make([]string, cnt)
	if len(this.buff) >= cnt {
		r, this.buff = this.buff[:], make([]string, 0)
	} else {
		r, this.buff = this.buff[0:cnt-1], this.buff[cnt:]
	}
	return r
}

func (this *Buff) DequeueALL() []string {
	cnt := this.Len()
	return this.BulkDequeue(cnt)
}

func (this *Buff) Len() int {
	this.mu.Lock()
	defer this.mu.Unlock()

	return len(this.buff)
}
