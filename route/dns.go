package route

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/dns"
	dnsOutbound "github.com/sagernet/sing-box/protocol/dns"
	R "github.com/sagernet/sing-box/route/rule"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	mDNS "github.com/miekg/dns"
)

func (r *Router) hijackDNSStream(ctx context.Context, conn net.Conn, metadata adapter.InboundContext) error {
	r.searchProcessInfo(ctx, &metadata)
	metadata.Destination = M.Socksaddr{}
	err := N.ReportConnHandshakeSuccess(conn, conn)
	if err != nil {
		return E.Cause(err, "report handshake success")
	}
	for {
		conn.SetReadDeadline(time.Now().Add(C.DNSTimeout))
		err = dnsOutbound.HandleStreamDNSRequest(ctx, r.dns, conn, metadata)
		if err != nil {
			if !E.IsClosedOrCanceled(err) {
				return err
			} else {
				return nil
			}
		}
	}
}

func (r *Router) hijackDNSPacket(ctx context.Context, conn N.PacketConn, packetBuffers []*N.PacketBuffer, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) error {
	diagnostics := formatDNSPacketContext(metadata, nil)
	r.searchProcessInfo(ctx, &metadata)
	err := N.ReportPacketConnHandshakeSuccess(conn, nil)
	if err != nil {
		N.ReleaseMultiPacketBuffer(packetBuffers)
		err = E.Cause(err, "report handshake success")
	} else {
		err = dnsOutbound.NewDNSPacketConnection(ctx, r.dns, conn, packetBuffers, metadata)
	}
	N.CloseOnHandshakeFailure(conn, onClose, err)
	if err != nil && !E.IsClosedOrCanceled(err) {
		return E.Cause(err, "process DNS packet", diagnostics)
	}
	return nil
}

func (r *Router) HijackDNSPacket(ctx context.Context, payload []byte, writer N.PacketWriter, metadata adapter.InboundContext) {
	diagnostics := formatDNSPacketContext(metadata, nil)
	var message mDNS.Msg
	err := message.Unpack(payload)
	if err != nil {
		r.logger.ErrorContext(ctx, E.Cause(err, "process DNS packet", diagnostics, " at unpack request (length=", len(payload), ")"))
		return
	}
	diagnostics = formatDNSPacketContext(metadata, &message)
	r.searchProcessInfo(ctx, &metadata)
	destination := metadata.Destination
	metadata.Destination = M.Socksaddr{}
	r.dns.ExchangeAsync(adapter.WithContext(ctx, &metadata), &message, adapter.DNSQueryOptions{}, func(response *mDNS.Msg, exchangeErr error) {
		if exchangeErr == nil {
			exchangeErr = r.writeDNSPacketResponse(&message, response, writer, destination)
		}
		if exchangeErr != nil && !R.IsRejected(exchangeErr) && !E.IsClosedOrCanceled(exchangeErr) {
			r.logger.ErrorContext(ctx, E.Cause(exchangeErr, "process DNS packet", diagnostics))
		}
	})
}

func (r *Router) writeDNSPacketResponse(message *mDNS.Msg, response *mDNS.Msg, writer N.PacketWriter, destination M.Socksaddr) error {
	responseBuffer, err := dns.TruncateDNSMessage(message, response, N.CalculateFrontHeadroom(writer), N.CalculateRearHeadroom(writer))
	if err != nil {
		return E.Cause(err, "pack DNS response")
	}
	err = writer.WritePacket(responseBuffer, destination)
	if err != nil {
		return E.Cause(err, "write DNS response to ", destination)
	}
	return nil
}

func formatDNSPacketContext(metadata adapter.InboundContext, message *mDNS.Msg) string {
	var fields []string
	if metadata.Network != "" {
		fields = append(fields, connectionDiagnosticField("network", metadata.Network))
	}
	if metadata.InboundType != "" || metadata.Inbound != "" {
		inbound := metadata.InboundType
		if metadata.Inbound != "" {
			inbound += "[" + metadata.Inbound + "]"
		}
		fields = append(fields, connectionDiagnosticField("inbound", inbound))
	}
	if metadata.Source.IsValid() {
		fields = append(fields, connectionDiagnosticField("source", metadata.Source.String()))
	}
	if metadata.Destination.IsValid() {
		fields = append(fields, connectionDiagnosticField("destination", metadata.Destination.String()))
	}
	if metadata.RouteOriginalDestination.IsValid() && metadata.RouteOriginalDestination != metadata.Destination {
		fields = append(fields, connectionDiagnosticField("original_destination", metadata.RouteOriginalDestination.String()))
	}
	if message != nil {
		question := "<empty>"
		if len(message.Question) > 0 {
			question = dns.FormatQuestion(message.Question[0].String())
		}
		fields = append(fields, connectionDiagnosticField("query", question))
	}
	if len(fields) == 0 {
		return ""
	}
	return " (" + strings.Join(fields, ", ") + ")"
}
