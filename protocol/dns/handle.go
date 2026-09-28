package dns

import (
	"context"
	"encoding/binary"
	"net"
	"strconv"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	"github.com/sagernet/sing/common/canceler"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/task"

	mDNS "github.com/miekg/dns"
)

func HandleStreamDNSRequest(ctx context.Context, router adapter.DNSRouter, conn net.Conn, metadata adapter.InboundContext) error {
	var queryLength uint16
	err := binary.Read(conn, binary.BigEndian, &queryLength)
	if err != nil {
		return E.Cause(err, "read TCP DNS request length (source=", formatDNSRequestSource(metadata), ")")
	}
	if queryLength == 0 {
		return E.Cause(dns.RcodeFormatError, "validate TCP DNS request length (source=", formatDNSRequestSource(metadata), ", declared_length=0)")
	}
	buffer := buf.NewSize(int(queryLength))
	defer buffer.Release()
	_, err = buffer.ReadFullFrom(conn, int(queryLength))
	if err != nil {
		return E.Cause(err, "read TCP DNS request payload (source=", formatDNSRequestSource(metadata), ", declared_length=", queryLength, ")")
	}
	var message mDNS.Msg
	err = message.Unpack(buffer.Bytes())
	if err != nil {
		return E.Cause(err, "unpack TCP DNS request (source=", formatDNSRequestSource(metadata), ", length=", queryLength, ")")
	}
	metadataInQuery := metadata
	router.ExchangeAsync(adapter.WithContext(ctx, &metadataInQuery), &message, adapter.DNSQueryOptions{}, func(response *mDNS.Msg, err error) {
		if err != nil {
			conn.Close()
			return
		}
		go writeStreamResponse(conn, response)
	})
	return nil
}

func writeStreamResponse(conn net.Conn, response *mDNS.Msg) {
	responseLength := response.Len()
	responseBuffer := buf.NewSize(3 + responseLength)
	defer responseBuffer.Release()
	responseBuffer.Resize(2, 0)
	n, err := response.PackBuffer(responseBuffer.FreeBytes())
	if err != nil {
		return
	}
	responseBuffer.Truncate(len(n))
	binary.BigEndian.PutUint16(responseBuffer.ExtendHeader(2), uint16(len(n)))
	conn.Write(responseBuffer.Bytes())
}

func NewDNSPacketConnection(ctx context.Context, router adapter.DNSRouter, conn N.PacketConn, cachedPackets []*N.PacketBuffer, metadata adapter.InboundContext) error {
	metadata.Destination = M.Socksaddr{}
	frontHeadroom := N.CalculateFrontHeadroom(conn)
	rearHeadroom := N.CalculateRearHeadroom(conn)
	var reader N.PacketReader = conn
	var counters []N.CountFunc
	cachedPackets = common.Reverse(cachedPackets)
	for {
		reader, counters = N.UnwrapCountPacketReader(reader, counters)
		if cachedReader, isCached := reader.(N.CachedPacketReader); isCached {
			packet := cachedReader.ReadCachedPacket()
			if packet != nil {
				cachedPackets = append(cachedPackets, packet)
				continue
			}
		}
		if readWaiter, created := bufio.CreatePacketReadWaiter(reader); created {
			readWaiter.InitializeReadWaiter(N.ReadWaitOptions{})
			return newDNSPacketConnection(ctx, router, conn, readWaiter, counters, cachedPackets, metadata)
		}
		break
	}
	fastClose, cancel := context.WithCancelCause(ctx)
	timeout := canceler.New(fastClose, cancel, C.DNSTimeout)
	var group task.Group
	group.Append0(func(_ context.Context) error {
		for {
			var message mDNS.Msg
			var destination M.Socksaddr
			var err error
			if len(cachedPackets) > 0 {
				packet := cachedPackets[0]
				cachedPackets = cachedPackets[1:]
				for _, counter := range counters {
					counter(int64(packet.Buffer.Len()))
				}
				packetLength := packet.Buffer.Len()
				destination = packet.Destination
				err = message.Unpack(packet.Buffer.Bytes())
				packet.Buffer.Release()
				if err != nil {
					err = E.Cause(err, "unpack UDP DNS request (destination=", quoteDNSValue(destination.String()), ", length=", packetLength, ")")
					cancel(err)
					return err
				}
			} else {
				buffer := buf.NewPacket()
				destination, err = conn.ReadPacket(buffer)
				if err != nil {
					buffer.Release()
					err = E.Cause(err, "read UDP DNS request (source=", formatDNSRequestSource(metadata), ")")
					cancel(err)
					return err
				}
				for _, counter := range counters {
					counter(int64(buffer.Len()))
				}
				packetLength := buffer.Len()
				err = message.Unpack(buffer.Bytes())
				buffer.Release()
				if err != nil {
					err = E.Cause(err, "unpack UDP DNS request (destination=", quoteDNSValue(destination.String()), ", length=", packetLength, ")")
					cancel(err)
					return err
				}
				timeout.Update()
			}
			metadataInQuery := metadata
			router.ExchangeAsync(adapter.WithContext(ctx, &metadataInQuery), &message, adapter.DNSQueryOptions{}, func(response *mDNS.Msg, err error) {
				if err != nil {
					err = E.Cause(err, "exchange DNS request (query=", formatDNSQuestion(&message), ")")
					cancel(err)
					return
				}
				timeout.Update()
				responseBuffer, truncateErr := dns.TruncateDNSMessage(&message, response, frontHeadroom, rearHeadroom)
				if truncateErr != nil {
					truncateErr = E.Cause(truncateErr, "pack DNS response (query=", formatDNSQuestion(&message), ")")
					cancel(truncateErr)
					return
				}
				writeErr := conn.WritePacket(responseBuffer, destination)
				if writeErr != nil {
					writeErr = E.Cause(writeErr, "write DNS response (query=", formatDNSQuestion(&message), ", destination=", quoteDNSValue(destination.String()), ")")
					cancel(writeErr)
				}
			})
		}
	})
	group.Cleanup(func() {
		conn.Close()
	})
	return group.Run(fastClose)
}

func newDNSPacketConnection(ctx context.Context, router adapter.DNSRouter, conn N.PacketConn, readWaiter N.PacketReadWaiter, readCounters []N.CountFunc, cached []*N.PacketBuffer, metadata adapter.InboundContext) error {
	frontHeadroom := N.CalculateFrontHeadroom(conn)
	rearHeadroom := N.CalculateRearHeadroom(conn)
	fastClose, cancel := context.WithCancelCause(ctx)
	timeout := canceler.New(fastClose, cancel, C.DNSTimeout)
	var group task.Group
	group.Append0(func(_ context.Context) error {
		for {
			var (
				message     mDNS.Msg
				destination M.Socksaddr
				err         error
				buffer      *buf.Buffer
			)
			if len(cached) > 0 {
				packet := cached[0]
				cached = cached[1:]
				for _, counter := range readCounters {
					counter(int64(packet.Buffer.Len()))
				}
				packetLength := packet.Buffer.Len()
				err = message.Unpack(packet.Buffer.Bytes())
				packet.Buffer.Release()
				destination = packet.Destination
				N.PutPacketBuffer(packet)
				if err != nil {
					err = E.Cause(err, "unpack UDP DNS request (destination=", quoteDNSValue(destination.String()), ", length=", packetLength, ")")
					cancel(err)
					return err
				}
			} else {
				buffer, destination, err = readWaiter.WaitReadPacket()
				if err != nil {
					err = E.Cause(err, "read UDP DNS request (source=", formatDNSRequestSource(metadata), ")")
					cancel(err)
					return err
				}
				for _, counter := range readCounters {
					counter(int64(buffer.Len()))
				}
				packetLength := buffer.Len()
				err = message.Unpack(buffer.Bytes())
				buffer.Release()
				if err != nil {
					err = E.Cause(err, "unpack UDP DNS request (destination=", quoteDNSValue(destination.String()), ", length=", packetLength, ")")
					cancel(err)
					return err
				}
				timeout.Update()
			}
			metadataInQuery := metadata
			router.ExchangeAsync(adapter.WithContext(ctx, &metadataInQuery), &message, adapter.DNSQueryOptions{}, func(response *mDNS.Msg, err error) {
				if err != nil {
					err = E.Cause(err, "exchange DNS request (query=", formatDNSQuestion(&message), ")")
					cancel(err)
					return
				}
				timeout.Update()
				responseBuffer, truncateErr := dns.TruncateDNSMessage(&message, response, frontHeadroom, rearHeadroom)
				if truncateErr != nil {
					truncateErr = E.Cause(truncateErr, "pack DNS response (query=", formatDNSQuestion(&message), ")")
					cancel(truncateErr)
					return
				}
				writeErr := conn.WritePacket(responseBuffer, destination)
				if writeErr != nil {
					writeErr = E.Cause(writeErr, "write DNS response (query=", formatDNSQuestion(&message), ", destination=", quoteDNSValue(destination.String()), ")")
					cancel(writeErr)
				}
			})
		}
	})
	group.Cleanup(func() {
		conn.Close()
	})
	return group.Run(fastClose)
}

func formatDNSRequestSource(metadata adapter.InboundContext) string {
	if !metadata.Source.IsValid() {
		return quoteDNSValue("unknown")
	}
	return quoteDNSValue(metadata.Source.String())
}

func formatDNSQuestion(message *mDNS.Msg) string {
	if message == nil || len(message.Question) == 0 {
		return quoteDNSValue("<empty>")
	}
	return quoteDNSValue(dns.FormatQuestion(message.Question[0].String()))
}

func quoteDNSValue(value string) string {
	const maxRunes = 256
	valueRunes := []rune(value)
	if len(valueRunes) > maxRunes {
		value = string(valueRunes[:maxRunes]) + "..."
	}
	return strconv.Quote(value)
}
