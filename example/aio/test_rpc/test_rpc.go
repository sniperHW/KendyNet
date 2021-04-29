package test_rpc

import (
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/sniperHW/kendynet"
	codec "github.com/sniperHW/kendynet/example/codec"
	"github.com/sniperHW/kendynet/example/pb"
	"github.com/sniperHW/kendynet/example/testproto"
	"github.com/sniperHW/kendynet/rpc"
	"github.com/sniperHW/kendynet/socket/aio"
	connector "github.com/sniperHW/kendynet/socket/connector/aio"
	listener "github.com/sniperHW/kendynet/socket/listener/aio"
	"reflect"
	"runtime"
	"time"
	"unsafe"
)

var aioService *aio.SocketService = aio.NewSocketService(aio.ServiceOption{
	/*PollerCount:              2,
	WorkerPerPoller:          runtime.NumCPU() / 2,
	CompleteRoutinePerPoller: 4,*/
	PollerCount:              1,
	WorkerPerPoller:          runtime.NumCPU(),
	CompleteRoutinePerPoller: 4,
})

type TcpStreamChannel struct {
	session kendynet.StreamSession
	name    string
}

func NewTcpStreamChannel(sess kendynet.StreamSession) *TcpStreamChannel {
	r := &TcpStreamChannel{session: sess}
	r.name = sess.RemoteAddr().String() + "<->" + sess.LocalAddr().String()
	return r
}

func (this *TcpStreamChannel) SendRequest(message interface{}) error {
	return this.session.Send(message)
}

func (this *TcpStreamChannel) SendResponse(message interface{}) error {
	return this.session.Send(message)
}

func (this *TcpStreamChannel) Name() string {
	return this.name
}

func (this *TcpStreamChannel) UID() uint64 {
	return uint64(uintptr(unsafe.Pointer(this)))
}

func (this *TcpStreamChannel) GetSession() kendynet.StreamSession {
	return this.session
}

type TestEncoder struct {
}

func (this *TestEncoder) Encode(message rpc.RPCMessage) (interface{}, error) {
	if message.Type() == rpc.RPC_REQUEST {
		req := message.(*rpc.RPCRequest)
		request := &testproto.RPCRequest{
			Seq:      proto.Uint64(req.Seq),
			Method:   proto.String(req.Method),
			NeedResp: proto.Bool(req.NeedResp),
		}
		if req.Arg != nil {
			b, err := pb.Encode(req.Arg, nil, 1000)
			if err != nil {
				fmt.Printf("encode error: %s\n", err.Error())
				return nil, err
			}
			request.Arg = b.Bytes()
		}
		return request, nil
	} else {
		resp := message.(*rpc.RPCResponse)
		response := &testproto.RPCResponse{Seq: proto.Uint64(resp.Seq)}
		if resp.Err != nil {
			response.Err = proto.String(resp.Err.Error())
		}
		if resp.Ret != nil {
			b, err := pb.Encode(resp.Ret, nil, 1000)
			if err != nil {
				fmt.Printf("encode error: %s\n", err.Error())
				return nil, err
			}
			response.Ret = b.Bytes()
		}
		return response, nil
	}
}

type TestDecoder struct {
}

func (this *TestDecoder) Decode(o interface{}) (rpc.RPCMessage, error) {
	switch o.(type) {
	case *testproto.RPCRequest:
		req := o.(*testproto.RPCRequest)
		request := &rpc.RPCRequest{
			Seq:      req.GetSeq(),
			Method:   req.GetMethod(),
			NeedResp: req.GetNeedResp(),
		}
		if len(req.Arg) > 0 {
			var err error
			request.Arg, _, err = pb.Decode(req.Arg, 0, len(req.Arg), 1000)
			if err != nil {
				return nil, err
			}
		}

		return request, nil
	case *testproto.RPCResponse:
		resp := o.(*testproto.RPCResponse)
		response := &rpc.RPCResponse{Seq: resp.GetSeq()}
		if resp.Err != nil {
			response.Err = fmt.Errorf(resp.GetErr())
		}
		if len(resp.Ret) > 0 {
			var err error
			response.Ret, _, err = pb.Decode(resp.Ret, 0, len(resp.Ret), 1000)
			if err != nil {
				return nil, err
			}
		}
		return response, nil
	default:
		return nil, fmt.Errorf("invaild obj type:%s", reflect.TypeOf(o).String())
	}
}

type RPCServer struct {
	server   *rpc.RPCServer
	listener *listener.Listener
}

func NewRPCServer() *RPCServer {
	return &RPCServer{
		server: rpc.NewRPCServer(&TestDecoder{}, &TestEncoder{}),
	}
}

func (this *RPCServer) RegisterMethod(name string, method rpc.RPCMethodHandler) {
	this.server.RegisterMethod(name, method)
}

func (this *RPCServer) Serve(service string) error {
	var err error
	this.listener, err = listener.New(aioService, "tcp", service)
	if err != nil {
		return err
	}

	err = this.listener.Serve(func(session kendynet.StreamSession) {
		channel := NewTcpStreamChannel(session)
		session.SetEncoder(codec.NewPbEncoder(65535))
		session.SetInBoundProcessor(codec.NewPBReceiver(65535))
		session.SetRecvTimeout(5 * time.Second)
		session.BeginRecv(func(s kendynet.StreamSession, msg interface{}) {
			this.server.OnRPCMessage(channel, msg)
		})
	})
	return err
}

var client *rpc.RPCClient = rpc.NewClient(&TestDecoder{}, &TestEncoder{})

type Caller struct {
	client  *rpc.RPCClient
	channel rpc.RPCChannel
}

func NewCaller() *Caller {
	return &Caller{}
}

func (this *Caller) Dial(service string, timeout time.Duration) error {
	connector, err := connector.New(aioService, "tcp", service)
	session, err := connector.Dial(timeout)
	if err != nil {
		return err
	}
	this.channel = NewTcpStreamChannel(session)
	this.client = client //rpc.NewClient(&TestDecoder{}, &TestEncoder{})
	session.SetEncoder(codec.NewPbEncoder(65535))
	session.SetInBoundProcessor(codec.NewPBReceiver(65535))

	session.SetRecvTimeout(5 * time.Second)
	session.SetCloseCallBack(func(sess kendynet.StreamSession, reason error) {
		fmt.Println("channel close:", reason)
	})

	session.BeginRecv(func(s kendynet.StreamSession, msg interface{}) {
		this.client.OnRPCMessage(msg)
	})

	return nil
}

func (this *Caller) AsynCall(method string, arg interface{}, timeout time.Duration, cb rpc.RPCResponseHandler) {
	this.client.AsynCall(this.channel, method, arg, timeout, cb)
}

func (this *Caller) Call(method string, arg interface{}, timeout time.Duration) (interface{}, error) {
	return this.client.Call(this.channel, method, arg, timeout)
}

func init() {
	pb.Register(&testproto.Hello{}, 1)
	pb.Register(&testproto.World{}, 2)
	pb.Register(&testproto.RPCResponse{}, 3)
	pb.Register(&testproto.RPCRequest{}, 4)
}
