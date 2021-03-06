package main

import (
	"errors"
	"fmt"
	"github.com/sniperHW/kendynet"
	"github.com/sniperHW/kendynet/buffer"
	"github.com/sniperHW/kendynet/golog"
	connector "github.com/sniperHW/kendynet/socket/connector/tcp"
	listener "github.com/sniperHW/kendynet/socket/listener/tcp"
	"github.com/sniperHW/kendynet/timer"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

type encoder struct {
}

func (this *encoder) EnCode(o interface{}, b *buffer.Buffer) error {
	switch o.(type) {
	case string:
		b.AppendString(o.(string))
	case []byte:
		b.AppendBytes(o.([]byte))
	default:
		return errors.New("invaild o")
	}
	return nil
}

func server(service string) {

	go func() {
		http.ListenAndServe("0.0.0.0:6060", nil)
	}()

	clientcount := int32(0)
	bytescount := int32(0)
	packetcount := int32(0)

	timer.Repeat(time.Second, func(_ *timer.Timer, ctx interface{}) {
		tmp1 := atomic.LoadInt32(&bytescount)
		tmp2 := atomic.LoadInt32(&packetcount)
		atomic.StoreInt32(&bytescount, 0)
		atomic.StoreInt32(&packetcount, 0)
		fmt.Printf("clientcount:%d,transrfer:%d KB/s,packetcount:%d\n", atomic.LoadInt32(&clientcount), tmp1/1024, tmp2)
	}, nil)

	server, err := listener.New("tcp4", service)
	if server != nil {
		fmt.Printf("server running on:%s\n", service)
		err = server.Serve(func(session kendynet.StreamSession) {
			atomic.AddInt32(&clientcount, 1)
			session.SetRecvTimeout(time.Second * 5)
			session.SetEncoder(&encoder{})
			session.SetCloseCallBack(func(sess kendynet.StreamSession, reason error) {
				atomic.AddInt32(&clientcount, -1)
				fmt.Println("client close:", reason, session.GetUnderConn(), atomic.LoadInt32(&clientcount))
			})

			session.BeginRecv(func(s kendynet.StreamSession, msg interface{}) {
				atomic.AddInt32(&bytescount, int32(len(msg.([]byte))))
				atomic.AddInt32(&packetcount, int32(1))
				s.Send(msg)
			})

		})

		if nil != err {
			fmt.Printf("TcpServer start failed %s\n", err)
		}

	} else {
		fmt.Printf("NewTcpServer failed %s\n", err)
	}
}

func client(service string, count int) {

	client, err := connector.New("tcp4", service)

	if err != nil {
		fmt.Printf("NewTcpClient failed:%s\n", err.Error())
		return
	}

	for i := 0; i < count; i++ {
		session, err := client.Dial(time.Second * 10)
		if err != nil {
			fmt.Printf("Dial error:%s\n", err.Error())
		} else {
			session.SetEncoder(&encoder{})

			session.SetCloseCallBack(func(sess kendynet.StreamSession, reason error) {
				fmt.Printf("client close:%s\n", reason)
			})

			session.BeginRecv(func(s kendynet.StreamSession, msg interface{}) {
				s.Send(msg)
			})

			//send the first messge
			msg := "hello"
			session.Send(msg)
			session.Send(msg)
			session.Send(msg)
		}
	}
}

func main() {

	//f, _ := os.Create("profile_file")
	//pprof.StartCPUProfile(f)     // 开始cpu profile，结果写到文件f中
	//defer pprof.StopCPUProfile() // 结束profile

	outLogger := golog.NewOutputLogger("log", "kendynet", 1024*1024*1000)
	kendynet.InitLogger(golog.New("rpc", outLogger))

	if len(os.Args) < 3 {
		fmt.Printf("usage ./pingpong [server|client|both] ip:port clientcount\n")
		return
	}

	mode := os.Args[1]

	if !(mode == "server" || mode == "client" || mode == "both") {
		fmt.Printf("usage ./pingpong [server|client|both] ip:port clientcount\n")
		return
	}

	service := os.Args[2]

	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGINT) //监听指定信号
	if mode == "server" || mode == "both" {
		go server(service)
	}

	if mode == "client" || mode == "both" {
		if len(os.Args) < 4 {
			fmt.Printf("usage ./pingpong [server|client|both] ip:port clientcount\n")
			return
		}
		connectioncount, err := strconv.Atoi(os.Args[3])
		if err != nil {
			fmt.Printf(err.Error())
			return
		}
		//让服务器先运行
		time.Sleep(10000000)
		go client(service, connectioncount)

	}

	_ = <-c //阻塞直至有信号传入

	fmt.Println("exit")

	return

}
