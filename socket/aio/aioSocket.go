// +build darwin netbsd freebsd openbsd dragonfly linux

package aio

import (
	"container/list"
	"github.com/sniperHW/aiogo"
	"github.com/sniperHW/kendynet"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

const (
	started = (1 << 0)
	closed  = (1 << 1)
	wclosed = (1 << 2)
	rclosed = (1 << 3)
)

type AioReceiver interface {
	ReceiveAndUnpack(sess kendynet.StreamSession) (interface{}, error)
	AppendBytes(buff []byte)
	GetRecvBuff() []byte
	GetUnPackSize() int
}

type defaultReceiver struct {
	bytes  int
	buffer []byte
}

func (this *defaultReceiver) ReceiveAndUnpack(_ kendynet.StreamSession) (interface{}, error) {
	if 0 != this.bytes {
		msg := kendynet.NewByteBuffer(this.bytes)
		msg.AppendBytes(this.buffer[:this.bytes])
		this.bytes = 0
		return msg, nil
	} else {
		return nil, nil
	}
}

func (this *defaultReceiver) AppendBytes(buff []byte) {
	this.bytes = len(buff)
}

func (this *defaultReceiver) GetRecvBuff() []byte {
	return this.buffer
}

func (this *defaultReceiver) GetUnPackSize() int {
	return 0
}

type AioSocket struct {
	sync.Mutex
	muW              sync.Mutex
	ud               interface{}
	receiver         AioReceiver
	encoder          *kendynet.EnCoder
	flag             int32
	onClose          func(kendynet.StreamSession, string)
	onEvent          func(*kendynet.Event)
	aioConn          *aiogo.Conn
	sendBuffs        [][]byte
	pendingSend      *list.List
	watcher          *aiogo.Watcher
	sendLock         bool
	rcompleteQueue   *aiogo.CompleteQueue
	wcompleteQueue   *aiogo.CompleteQueue
	sendQueueSize    int
	onClearSendQueue func()
	closeReason      string
	maxPostSendSize  int
}

func NewAioSocket(netConn net.Conn) *AioSocket {

	w, rq, wq := getWatcherAndCompleteQueue()

	c, err := w.Watch(netConn)
	if err != nil {
		return nil
	}

	s := &AioSocket{
		aioConn:         c,
		watcher:         w,
		rcompleteQueue:  rq,
		wcompleteQueue:  wq,
		sendQueueSize:   256,
		sendBuffs:       make([][]byte, 512),
		pendingSend:     list.New(),
		maxPostSendSize: 1024 * 1024,
	}
	return s
}

func (this *AioSocket) dosend() {
	this.muW.Lock()
	c := 0
	totalSize := 0
	for v := this.pendingSend.Front(); v != nil; v = this.pendingSend.Front() {
		this.pendingSend.Remove(v)
		this.sendBuffs[c] = v.Value.(kendynet.Message).Bytes()
		totalSize += len(this.sendBuffs[c])
		c++
		if c >= len(this.sendBuffs) || totalSize >= this.maxPostSendSize {
			break
		}
	}

	this.muW.Unlock()

	if c > 0 {
		this.aioConn.SendBuffers(this.sendBuffs[:c], this, this.wcompleteQueue)
	}
}

func (this *AioSocket) onSendComplete(r *aiogo.CompleteEvent) {
	if nil == r.Err {
		this.muW.Lock()
		if this.pendingSend.Len() == 0 {
			this.sendLock = false
			onClearSendQueue := this.onClearSendQueue
			this.muW.Unlock()
			if nil != onClearSendQueue {
				onClearSendQueue()
			}
		} else {
			this.muW.Unlock()
			this.aioConn.PostClosure(this.dosend)
			//this.dosend()
		}
	} else {
		flag := this.getFlag()
		if !(flag&closed > 0) {
			this.onEvent(&kendynet.Event{
				Session:   this,
				EventType: kendynet.EventTypeError,
				Data:      r.Err,
			})
		}
	}
}

func (this *AioSocket) getFlag() int32 {
	this.Lock()
	defer this.Unlock()
	return this.flag
}

func (this *AioSocket) onRecvComplete(r *aiogo.CompleteEvent) {
	if nil != r.Err {
		flag := this.getFlag()
		if flag&closed > 0 || flag&rclosed > 0 {
			return
		} else {
			this.Lock()
			if r.Err == io.EOF {
				this.flag |= rclosed
			} else {
				this.flag |= (rclosed | wclosed)
			}
			this.Unlock()

			this.onEvent(&kendynet.Event{
				Session:   this,
				EventType: kendynet.EventTypeError,
				Data:      r.Err,
			})
		}
	} else {
		this.receiver.AppendBytes(r.Buff[0][:r.Size])
		for {
			flag := this.getFlag()
			if flag&closed > 0 || flag&rclosed > 0 {
				return
			}
			msg, err := this.receiver.ReceiveAndUnpack(this)
			if nil != err {
				this.onEvent(&kendynet.Event{
					Session:   this,
					EventType: kendynet.EventTypeError,
					Data:      err,
				})
			} else if msg != nil {
				this.onEvent(&kendynet.Event{
					Session:   this,
					EventType: kendynet.EventTypeMessage,
					Data:      msg,
				})
			} else {
				this.aioConn.Recv(this.receiver.GetRecvBuff(), this, this.rcompleteQueue)
				return
			}
		}
	}
}

func (this *AioSocket) Send(o interface{}) error {
	if o == nil {
		return kendynet.ErrInvaildObject
	}

	encoder := (*kendynet.EnCoder)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&this.encoder))))

	if nil == *encoder {
		return kendynet.ErrInvaildEncoder
	}

	msg, err := (*encoder).EnCode(o)

	if err != nil {
		return err
	}

	return this.sendMessage(msg)
}

func (this *AioSocket) sendMessage(msg kendynet.Message) error {
	send, err := func() (bool, error) {
		this.muW.Lock()
		defer this.muW.Unlock()
		if (this.flag&closed) > 0 || (this.flag&wclosed) > 0 {
			return false, kendynet.ErrSocketClose
		}

		if this.pendingSend.Len() > this.sendQueueSize {
			return false, kendynet.ErrSendQueFull
		}

		this.pendingSend.PushBack(msg)

		send := false

		if !this.sendLock {
			this.sendLock = true
			send = true
		}

		return send, nil
	}()

	if nil != err {
		return err
	}

	if send {
		this.aioConn.PostClosure(this.dosend)
		//this.wcompleteQueue.Post(&aiogo.CompleteEvent{
		//	Type: aiogo.User,
		//	Ud:   this.dosend,
		//})
	}
	return nil
}

func (this *AioSocket) SendMessage(msg kendynet.Message) error {
	if msg == nil {
		return kendynet.ErrInvaildObject
	}

	return this.sendMessage(msg)
}

func (this *AioSocket) doClose() {
	this.watcher.UnWatch(this.aioConn)
	this.aioConn.GetRowConn().Close()
	this.Lock()
	onClose := this.onClose
	this.Unlock()
	if nil != onClose {
		onClose(this, this.closeReason)
	}
}

func (this *AioSocket) Close(reason string, delay time.Duration) {
	this.Lock()
	if (this.flag & closed) > 0 {
		this.Unlock()
		return
	}

	this.closeReason = reason
	this.flag |= (closed | rclosed)
	if this.flag&wclosed > 0 {
		delay = 0 //写端已经关闭，delay参数没有意义设置为0
	}

	this.muW.Lock()
	if this.pendingSend.Len() > 0 {
		delay = delay * time.Second
		if delay <= 0 {
			this.pendingSend = list.New()
		}
	}
	this.muW.Unlock()

	var ch chan struct{}

	if delay > 0 {
		ch = make(chan struct{})
		this.onClearSendQueue = func() {
			close(ch)
		}
	}

	this.Unlock()

	if delay > 0 {
		this.shutdownRead()
		ticker := time.NewTicker(delay)
		go func() {
			/*
			 *	delay > 0,sendThread最多需要经过delay秒之后才会结束，
			 *	为了避免阻塞调用Close的goroutine,启动一个新的goroutine在chan上等待事件
			 */
			select {
			case <-ch:
			case <-ticker.C:
			}

			ticker.Stop()
			this.doClose()
		}()
	} else {
		this.doClose()
	}
}

func (this *AioSocket) IsClosed() bool {
	this.Lock()
	defer this.Unlock()
	return this.flag&closed > 0
}

func (this *AioSocket) shutdownRead() {
	underConn := this.GetUnderConn()
	switch underConn.(type) {
	case *net.TCPConn:
		underConn.(*net.TCPConn).CloseRead()
		break
	case *net.UnixConn:
		underConn.(*net.UnixConn).CloseRead()
		break
	}
}

func (this *AioSocket) ShutdownRead() {
	this.Lock()
	defer this.Unlock()
	if (this.flag & closed) > 0 {
		return
	}
	this.flag |= rclosed
	this.shutdownRead()
}

func (this *AioSocket) SetCloseCallBack(cb func(kendynet.StreamSession, string)) {
	this.Lock()
	defer this.Unlock()
	this.onClose = cb
}

/*
 *   设置接收解包器,必须在调用Start前设置，Start成功之后的调用将没有任何效果
 */
func (this *AioSocket) SetReceiver(r kendynet.Receiver) {
	if aio_r, ok := r.(AioReceiver); ok {
		this.Lock()
		defer this.Unlock()
		if (this.flag & started) > 0 {
			return
		}
		this.receiver = aio_r
	} else {
		panic("must use AioReceiver")
	}
}

func (this *AioSocket) SetEncoder(encoder kendynet.EnCoder) {
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&this.encoder)), unsafe.Pointer(&encoder))
}

func (this *AioSocket) Start(eventCB func(*kendynet.Event)) error {
	if eventCB == nil {
		panic("eventCB == nil")
	}

	this.Lock()
	defer this.Unlock()

	if (this.flag & closed) > 0 {
		return kendynet.ErrSocketClose
	}

	if (this.flag & started) > 0 {
		return kendynet.ErrStarted
	}

	if this.receiver == nil {
		this.receiver = &defaultReceiver{buffer: make([]byte, 4096)}
	}

	this.onEvent = eventCB
	this.flag |= started

	this.aioConn.Recv(this.receiver.GetRecvBuff(), this, this.rcompleteQueue)

	return nil
}

func (this *AioSocket) LocalAddr() net.Addr {
	return this.aioConn.GetRowConn().LocalAddr()
}

func (this *AioSocket) RemoteAddr() net.Addr {
	return this.aioConn.GetRowConn().RemoteAddr()
}

func (this *AioSocket) SetUserData(ud interface{}) {
	this.Lock()
	defer this.Unlock()
	this.ud = ud
}

func (this *AioSocket) GetUserData() (ud interface{}) {
	this.Lock()
	defer this.Unlock()
	return this.ud
}

func (this *AioSocket) GetUnderConn() interface{} {
	return this.aioConn.GetRowConn()
}

func (this *AioSocket) SetRecvTimeout(timeout time.Duration) {

}

func (this *AioSocket) SetSendTimeout(timeout time.Duration) {

}

func (this *AioSocket) SetMaxPostSendSize(size int) {
	this.muW.Lock()
	defer this.muW.Unlock()
	this.maxPostSendSize = size
}

func (this *AioSocket) SetSendQueueSize(size int) {
	this.muW.Lock()
	defer this.muW.Unlock()
	this.sendQueueSize = size
}
