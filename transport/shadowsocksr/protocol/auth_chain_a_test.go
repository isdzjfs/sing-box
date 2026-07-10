package protocol

import (
	"testing"

	"github.com/sagernet/sing-box/transport/shadowsocksr/tools"
)

func TestAuthChainADecodePacketRejectsOversizedPadding(t *testing.T) {
	protocol := newAuthChainA(&Base{
		Key:   []byte("test-key"),
		Param: "1:test-user-key",
	}).(*authChainA)
	packet := make([]byte, 9)
	var paddingLength int
	for value := 0; value < 256; value++ {
		packet[1] = byte(value)
		md5Data := tools.HmacMD5(protocol.Key, packet[1:8])
		paddingLength = udpGetRandLength(md5Data, &protocol.randomServer)
		if paddingLength > len(packet)-8 {
			break
		}
	}
	if paddingLength <= len(packet)-8 {
		t.Fatal("failed to construct oversized padding packet")
	}
	packet[len(packet)-1] = tools.HmacMD5(protocol.userKey, packet[:len(packet)-1])[0]

	if _, err := protocol.DecodePacket(packet); err != errAuthChainLengthError {
		t.Fatalf("expected %v, got %v", errAuthChainLengthError, err)
	}
}
