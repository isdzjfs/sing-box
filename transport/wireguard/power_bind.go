package wireguard

import (
	"github.com/sagernet/sing-box/service/powerreport"
	"github.com/sagernet/wireguard-go/conn"
)

var _ conn.Bind = (*powerReportBind)(nil)

type powerReportBind struct {
	conn.Bind
	recorder    *powerreport.Recorder
	attribution *powerreport.Attribution
}

func (b *powerReportBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	receiveFuncs, actualPort, err := b.Bind.Open(port)
	if err != nil {
		return receiveFuncs, actualPort, err
	}
	wrappedFuncs := make([]conn.ReceiveFunc, len(receiveFuncs))
	for i, receiveFunc := range receiveFuncs {
		wrappedFuncs[i] = func(packets [][]byte, sizes []int, endpoints []conn.Endpoint) (int, error) {
			count, receiveErr := receiveFunc(packets, sizes, endpoints)
			if count > 0 {
				b.recorder.Touch(powerreport.DirectionInbound, sizes[0], b.attribution)
			}
			return count, receiveErr
		}
	}
	return wrappedFuncs, actualPort, nil
}

func (b *powerReportBind) Send(buffers [][]byte, endpoint conn.Endpoint, offset int) error {
	if len(buffers) > 0 {
		b.recorder.Touch(powerreport.DirectionOutbound, len(buffers[0])-offset, b.attribution)
	}
	return b.Bind.Send(buffers, endpoint, offset)
}
