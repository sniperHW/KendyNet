package codec

import (
	"github.com/sniperHW/kendynet"
	"github.com/sniperHW/kendynet/example/pb"
	//"github.com/sniperHW/kendynet/socket"
	//"os"
	//"time"
)

const minBuffSize = 4096

type PbEncoder struct {
	maxMsgSize uint64
}

func NewPbEncoder(maxMsgSize uint64) *PbEncoder {
	return &PbEncoder{maxMsgSize: maxMsgSize}
}

func (this *PbEncoder) EnCode(o interface{}) (kendynet.Message, error) {
	return pb.Encode(o, this.maxMsgSize)
}

type PBReceiver struct {
	buffer         []byte
	maxpacket      int
	unpackSize     int
	unpackIdx      int
	initBuffSize   int
	totalMaxPacket int
}

func NewPBReceiver(maxMsgSize int) *PBReceiver {
	receiver := &PBReceiver{}
	//完整数据包大小为head+data
	receiver.totalMaxPacket = maxMsgSize + int(pb.PBHeaderSize)
	doubleTotalPacketSize := receiver.totalMaxPacket * 2
	if doubleTotalPacketSize < minBuffSize {
		receiver.initBuffSize = minBuffSize
	} else {
		receiver.initBuffSize = doubleTotalPacketSize
	}
	receiver.buffer = make([]byte, receiver.initBuffSize)
	receiver.maxpacket = maxMsgSize
	return receiver
}

func (this *PBReceiver) unPack() (interface{}, error) {
	msg, dataLen, err := pb.Decode(this.buffer, uint64(this.unpackIdx), uint64(this.unpackIdx+this.unpackSize), uint64(this.maxpacket))
	if dataLen > 0 {
		this.unpackIdx += int(dataLen)
		this.unpackSize -= int(dataLen)
	}
	return msg, err
}

func (this *PBReceiver) AppendBytes(buff []byte) {
	/*capRemain := len(this.buffer) - this.unpackSize
	if capRemain < len(buff) {
		newBuff := make([]byte, len(this.buffer)+len(buff)-capRemain)
		copy(newBuff, this.buffer[:this.unpackSize])
	}
	copy(this.buffer[this.unpackSize:], buff)
	this.unpackSize += len(buff)*/
	this.buffer = buff
	this.unpackSize = len(buff)
}

func (this *PBReceiver) ReceiveAndUnpack(sess kendynet.StreamSession) (interface{}, error) {
	msg, err := this.unPack()

	if nil != msg {
		//if this.unpackSize > 0 {
		//有数据尚未解包，需要移动到buffer前部
		//	copy(this.buffer, this.buffer[this.unpackIdx:this.unpackIdx+this.unpackSize])
		//}
		this.unpackIdx = 0
	}

	return msg, err
}
