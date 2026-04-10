package capture

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/google/gopacket"
	"github.com/google/gopacket/afpacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/tcpassembly"
	"github.com/google/gopacket/tcpassembly/tcpreader"
	"github.com/vinayak/sniffit/internal/alerting"
	"github.com/vinayak/sniffit/internal/amqp"
	"github.com/vinayak/sniffit/internal/rules"
)

type Sniffer struct {
	iface      string
	ports      []int
	targetHost string
	engine     *rules.Engine
	dispatcher *alerting.Dispatcher
}

func NewSniffer(iface string, ports []int, targetHost string, engine *rules.Engine, dispatcher *alerting.Dispatcher) *Sniffer {
	return &Sniffer{
		iface:      iface,
		ports:      ports,
		targetHost: targetHost,
		engine:     engine,
		dispatcher: dispatcher,
	}
}

// detectLinkLayer determines the correct gopacket decoder for a given Linux
// network interface.
//
// On Linux with AF_PACKET (ETH_P_ALL), ALL interfaces — including loopback —
// present raw Ethernet frames (DLT_EN10MB, 14-byte header: dst+src MAC +
// EtherType). This is fundamentally different from BSD/macOS where the
// loopback interface uses DLT_NULL (a 4-byte protocol-family header).
//
// Using layers.LayerTypeLoopback on Linux is WRONG: it would try to parse the
// first 4 bytes of an Ethernet frame as a BSD protocol family value, fail to
// identify IPv4/IPv6, and return nil for every TCP/IP layer extraction.
//
// Rule: on Linux, always use layers.LayerTypeEthernet for AF_PACKET capture,
// regardless of whether the interface is eth0, lo, docker0, etc.
func detectLinkLayer(iface string) gopacket.Decoder {
	// Attempt to detect via interface flags for clarity; default to Ethernet.
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		log.Printf("[sniffer] Could not look up interface %q, defaulting to Ethernet: %v", iface, err)
		return layers.LayerTypeEthernet
	}

	log.Printf("[sniffer] Interface %q — flags: %v, HW: %s", iface, ifi.Flags, ifi.HardwareAddr)

	// On Linux, AF_PACKET on loopback presents Ethernet frames with zero MACs.
	// DO NOT use LayerTypeLoopback — that is BSD/macOS only (DLT_NULL).
	return layers.LayerTypeEthernet
}

func (s *Sniffer) Start() error {
	tpacket, err := afpacket.NewTPacket(
		afpacket.OptInterface(s.iface),
	)
	if err != nil {
		return fmt.Errorf("failed to open AF_PACKET on %s: %w", s.iface, err)
	}
	defer tpacket.Close()

	firstLayer := detectLinkLayer(s.iface)
	log.Printf("[sniffer] Starting capture on %s | link-layer: %v | ports: %v | target: %q",
		s.iface, firstLayer, s.ports, s.targetHost)

	source := gopacket.NewPacketSource(tpacket, firstLayer)
	source.NoCopy = true

	streamFactory := &multiProtocolStreamFactory{
		ports:      s.ports,
		engine:     s.engine,
		dispatcher: s.dispatcher,
	}
	streamPool := tcpassembly.NewStreamPool(streamFactory)
	assembler := tcpassembly.NewAssembler(streamPool)

	packetCount := 0
	matchCount := 0

	for packet := range source.Packets() {
		packetCount++

		// Log the first packet and every 100 thereafter to confirm capture is alive.
		if packetCount == 1 || packetCount%100 == 0 {
			log.Printf("[sniffer] HEARTBEAT: total=%d matched=%d", packetCount, matchCount)
		}

		// Log first few packets in detail so we can see what layers are present.
		if packetCount <= 5 {
			logPacketLayers(packet, packetCount)
		}

		netLayer := packet.NetworkLayer()
		if netLayer == nil {
			continue
		}

		tcpLayer := packet.Layer(layers.LayerTypeTCP)
		if tcpLayer == nil {
			continue
		}
		tcp, _ := tcpLayer.(*layers.TCP)

		matchPort := false
		for _, p := range s.ports {
			if int(tcp.SrcPort) == p || int(tcp.DstPort) == p {
				matchPort = true
				break
			}
		}
		if !matchPort {
			continue
		}

		// Optional target-host filter
		if s.targetHost != "" {
			flow := netLayer.NetworkFlow()
			if flow.Src().String() != s.targetHost && flow.Dst().String() != s.targetHost {
				continue
			}
		}

		matchCount++
		if matchCount <= 10 || matchCount%50 == 0 {
			tcp2 := tcp
			log.Printf("[sniffer] Matched TCP pkt #%d: src=%d dst=%d SYN=%v FIN=%v len=%d",
				matchCount, tcp2.SrcPort, tcp2.DstPort, tcp2.SYN, tcp2.FIN, len(tcp2.Payload))
		}

		assembler.AssembleWithTimestamp(netLayer.NetworkFlow(), tcp, packet.Metadata().Timestamp)
	}

	return nil
}

// logPacketLayers prints the decoded layer chain of a packet for debugging.
func logPacketLayers(pkt gopacket.Packet, n int) {
	layerNames := []string{}
	for _, l := range pkt.Layers() {
		layerNames = append(layerNames, l.LayerType().String())
	}
	errStr := ""
	if pkt.ErrorLayer() != nil {
		errStr = fmt.Sprintf(" | ERROR: %v", pkt.ErrorLayer().Error())
	}
	log.Printf("[sniffer] Packet #%d layers: %v%s", n, layerNames, errStr)
}

// ── Stream Factory ────────────────────────────────────────────────────────────

type multiProtocolStreamFactory struct {
	ports      []int
	engine     *rules.Engine
	dispatcher *alerting.Dispatcher
}

func (f *multiProtocolStreamFactory) New(net, transport gopacket.Flow) tcpassembly.Stream {
	dstPort, _ := strconv.Atoi(transport.Dst().String())
	srcPort, _ := strconv.Atoi(transport.Src().String())
	flowID := fmt.Sprintf("%s:%s", net, transport)

	// Determine which protocol port this stream belongs to.
	// DST port wins (new connection direction); fall back to SRC port (response direction).
	port := dstPort
	if !f.isTargetPort(port) {
		port = srcPort
	}

	r := tcpreader.NewReaderStream()

	if port == 15672 {
		s := &httpStream{r: r, engine: f.engine, dispatcher: f.dispatcher, flow: flowID}
		go s.run()
		return &s.r
	}

	// Default to AMQP for any other monitored port (e.g., 5672)
	s := &amqpStream{r: r, engine: f.engine, dispatcher: f.dispatcher, flow: flowID}
	go s.run()
	return &s.r
}

func (f *multiProtocolStreamFactory) isTargetPort(p int) bool {
	for _, target := range f.ports {
		if p == target {
			return true
		}
	}
	return false
}

// ── AMQP Stream ───────────────────────────────────────────────────────────────

type amqpStream struct {
	r          tcpreader.ReaderStream
	engine     *rules.Engine
	dispatcher *alerting.Dispatcher
	flow       string
}

func (s *amqpStream) run() {
	// FIX: Always drain the ReaderStream on exit.
	// tcpreader.ReaderStream contract: the consumer MUST read all bytes
	// before returning, otherwise the assembler stalls holding internal buffers forever.
	defer io.Copy(io.Discard, &s.r) //nolint:errcheck

	// FIX: The AMQP client sends an 8-byte protocol header at the very start
	// of a new TCP connection BEFORE any frames:
	//   'A','M','Q','P', 0, 0, 9, 1
	// Without consuming this header first, the parser reads it as a frame
	// header, causing permanent stream misalignment and zero decoded frames.
	//
	// The server side does NOT send this header — it starts directly with
	// Connection.Start frame. We detect which side this is by peeking at the
	// first 8 bytes and checking whether they spell "AMQP".
	headerBuf := make([]byte, 8)
	if _, err := io.ReadFull(&s.r, headerBuf); err != nil {
		// Stream ended before we could read anything — normal for short flows.
		return
	}

	var frameReader io.Reader
	if headerBuf[0] == 'A' && headerBuf[1] == 'M' && headerBuf[2] == 'Q' && headerBuf[3] == 'P' {
		// Client side: protocol header consumed, remaining stream is pure frames.
		log.Printf("[amqp] %s — client stream, protocol header OK (v%d.%d.%d)",
			s.flow, headerBuf[5], headerBuf[6], headerBuf[7])
		frameReader = &s.r
	} else {
		// Server side: the 8 bytes we already read are the start of a frame.
		// Push them back using a MultiReader so the parser sees a contiguous stream.
		log.Printf("[amqp] %s — server stream (no header), pushing 8 bytes back", s.flow)
		frameReader = io.MultiReader(bytes.NewReader(headerBuf), &s.r)
	}

	s.runFrameLoop(frameReader)
}

func (s *amqpStream) runFrameLoop(r io.Reader) {
	parser := amqp.NewParser(r)
	frameCount := 0

	for {
		frame, err := parser.NextFrame()
		if err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				log.Printf("[amqp] %s — stream ended after %d frames: %v", s.flow, frameCount, err)
			}
			return
		}
		frameCount++

		// Only method frames carry evaluatable AMQP commands.
		// Header (2), body (3), and heartbeat (8) frames are normal — skip.
		if frame.Type != amqp.FrameMethod {
			continue
		}

		method, err := frame.UnmarshalMethod()
		if err != nil {
			log.Printf("[amqp] %s — failed to unmarshal method: %v", s.flow, err)
			continue
		}

		log.Printf("[amqp] %s — frame #%d Class=%d Method=%d", s.flow, frameCount, method.ClassID, method.MethodID)

		alert := s.engine.EvaluateMethod(method)
		if alert != nil {
			log.Printf("[amqp] RULE HIT on %s — rule=%s severity=%s", s.flow, alert.RuleName, alert.Severity)
			s.dispatcher.Dispatch(alert)
		}
	}
}

// ── HTTP Stream ───────────────────────────────────────────────────────────────

type httpStream struct {
	r          tcpreader.ReaderStream
	engine     *rules.Engine
	dispatcher *alerting.Dispatcher
	flow       string
}

func (s *httpStream) run() {
	// FIX: Always drain the ReaderStream on exit.
	defer io.Copy(io.Discard, &s.r) //nolint:errcheck

	buf := bufio.NewReader(&s.r)
	respCount := 0

	for {
		resp, err := http.ReadResponse(buf, nil)
		if err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				log.Printf("[http] %s — stream ended after %d responses: %v", s.flow, respCount, err)
			}
			return
		}
		respCount++

		// FIX: http.ReadResponse requires the previous response body to be
		// FULLY CONSUMED before the next ReadResponse call can succeed.
		// resp.Body.Close() on a tcpreader.ReaderStream does NOT drain automatically.
		// Without io.ReadAll here, the second call to ReadResponse stalls permanently.
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		_ = body

		log.Printf("[http] %s — response #%d status=%d", s.flow, respCount, resp.StatusCode)

		alert := s.engine.EvaluateHTTPResponse(resp)
		if alert != nil {
			log.Printf("[http] RULE HIT on %s — rule=%s severity=%s", s.flow, alert.RuleName, alert.Severity)
			s.dispatcher.Dispatch(alert)
		}
	}
}
