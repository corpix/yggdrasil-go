package core

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/apernet/quic-go/quicvarint"
	xraynet "github.com/xtls/xray-core/common/net"
	xraycnc "github.com/xtls/xray-core/common/net/cnc"
	xrayinternet "github.com/xtls/xray-core/transport/internet"
	xrayhysteria "github.com/xtls/xray-core/transport/internet/hysteria"
	xraycongestion "github.com/xtls/xray-core/transport/internet/hysteria/congestion"
	xraybbr "github.com/xtls/xray-core/transport/internet/hysteria/congestion/bbr"
	xraystat "github.com/xtls/xray-core/transport/internet/stat"
	xraytls "github.com/xtls/xray-core/transport/internet/tls"
)

type linkXrayHysteria struct {
	*links
}

type hysteriaPeerConfig struct {
	password string
	sni      string
	fp       string
	remote   ed25519.PublicKey
	stream   *xrayinternet.MemoryStreamConfig
}

type hysteriaListenerConfig struct {
	password string
	sni      string
	stream   *xrayinternet.MemoryStreamConfig
}

type linkXrayHysteriaListener struct {
	ctx    context.Context
	cancel context.CancelFunc
	inner  xrayinternet.Listener
	ch     chan net.Conn
}

type linkXrayHysteriaConn struct {
	stream    *quic.Stream
	conn      *quic.Conn
	transport *quic.Transport
	pktConn   net.PacketConn
	local     net.Addr
	remote    net.Addr
	client    bool
}

func (l *links) newLinkXrayHysteria() *linkXrayHysteria {
	return &linkXrayHysteria{links: l}
}

func (l *linkXrayHysteria) dial(ctx context.Context, u *url.URL, info linkInfo, options linkOptions) (net.Conn, error) {
	cfg, err := l.parsePeerConfig(u)
	if err != nil {
		return nil, err
	}

	return l.findSuitableIP(u, func(hostname string, ip net.IP, port int) (net.Conn, error) {
		dest := xraynet.UDPDestination(xraynet.IPAddress(ip), xraynet.Port(port))
		conn, err := dialHysteriaStream(ctx, dest, cfg)
		if err != nil {
			return nil, err
		}
		return conn, nil
	})
}

func (l *linkXrayHysteria) listen(ctx context.Context, u *url.URL, _ string) (net.Listener, error) {
	cfg, err := l.parseListenerConfig(u)
	if err != nil {
		return nil, err
	}

	host, port, err := splitHostPort(u.Host)
	if err != nil {
		return nil, err
	}

	address := xraynet.ParseAddress(host)
	if address == nil {
		return nil, fmt.Errorf("xray+hysteria: invalid listen host %q", host)
	}

	listenerCtx, cancel := context.WithCancel(ctx)
	ch := make(chan net.Conn)
	inner, err := xrayhysteria.Listen(listenerCtx, address, xraynet.Port(port), cfg.stream, func(conn xraystat.Connection) {
		select {
		case ch <- conn:
		case <-listenerCtx.Done():
			_ = conn.Close()
		}
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("xray+hysteria: listen: %w", err)
	}

	return &linkXrayHysteriaListener{
		ctx:    listenerCtx,
		cancel: cancel,
		inner:  inner,
		ch:     ch,
	}, nil
}

func (l *linkXrayHysteria) parsePeerConfig(u *url.URL) (*hysteriaPeerConfig, error) {
	q := u.Query()

	password := q.Get("password")
	if password == "" {
		return nil, fmt.Errorf("xray+hysteria: missing required param 'password'")
	}

	keyHex := q.Get("key")
	if keyHex == "" {
		return nil, fmt.Errorf("xray+hysteria: missing required param 'key'")
	}
	key, err := decodeEd25519PublicKey(keyHex)
	if err != nil {
		return nil, fmt.Errorf("xray+hysteria: invalid 'key': %w", err)
	}

	stream, sni, fp, err := buildHysteriaStreamSettings(q, l.core.secret, false, password)
	if err != nil {
		return nil, err
	}

	return &hysteriaPeerConfig{
		password: password,
		sni:      sni,
		fp:       fp,
		remote:   key,
		stream:   stream,
	}, nil
}

func (l *linkXrayHysteria) parseListenerConfig(u *url.URL) (*hysteriaListenerConfig, error) {
	q := u.Query()

	password := q.Get("password")
	if password == "" {
		return nil, fmt.Errorf("xray+hysteria: missing required param 'password'")
	}

	stream, sni, _, err := buildHysteriaStreamSettings(q, l.core.secret, true, password)
	if err != nil {
		return nil, err
	}

	return &hysteriaListenerConfig{
		password: password,
		sni:      sni,
		stream:   stream,
	}, nil
}

func buildHysteriaStreamSettings(q url.Values, nodeKey ed25519.PrivateKey, server bool, password string) (*xrayinternet.MemoryStreamConfig, string, string, error) {
	quicParams, err := parseHysteriaQUICParams(q)
	if err != nil {
		return nil, "", "", err
	}

	sni := q.Get("sni")

	fp := strings.ToLower(strings.TrimSpace(q.Get("fp")))
	if fp != "" && xraytls.GetFingerprint(fp) == nil {
		return nil, "", "", fmt.Errorf("xray+hysteria: invalid 'fp': unsupported fingerprint %q", q.Get("fp"))
	}

	tlsConfig, err := buildHysteriaTLSConfig(nodeKey, sni, fp, server)
	if err != nil {
		return nil, "", "", err
	}

	hysteriaConfig := &xrayhysteria.Config{
		Auth:           password,
		UdpIdleTimeout: 60,
	}

	if server {
		hysteriaConfig.MasqType = q.Get("masqType")
		hysteriaConfig.MasqFile = q.Get("masqFile")
		hysteriaConfig.MasqUrl = q.Get("masqURL")
		if v := q.Get("masqURLRewriteHost"); v != "" {
			hysteriaConfig.MasqUrlRewriteHost, err = parseBoolParam("masqURLRewriteHost", v)
			if err != nil {
				return nil, "", "", err
			}
		}
		if v := q.Get("masqURLInsecure"); v != "" {
			hysteriaConfig.MasqUrlInsecure, err = parseBoolParam("masqURLInsecure", v)
			if err != nil {
				return nil, "", "", err
			}
		}
		if v := q.Get("masqString"); v != "" {
			hysteriaConfig.MasqType = "string"
			hysteriaConfig.MasqString = v
		}
		if v := q.Get("masqStringStatusCode"); v != "" {
			code, parseErr := parseInt32Param("masqStringStatusCode", v)
			if parseErr != nil {
				return nil, "", "", parseErr
			}
			hysteriaConfig.MasqStringStatusCode = code
		}
	}

	return &xrayinternet.MemoryStreamConfig{
		ProtocolName:     "hysteria",
		ProtocolSettings: hysteriaConfig,
		SecurityType:     "tls",
		SecuritySettings: tlsConfig,
		QuicParams:       quicParams,
	}, sni, fp, nil
}

func buildHysteriaTLSConfig(nodeKey ed25519.PrivateKey, sni, fp string, server bool) (*xraytls.Config, error) {
	certPEM, keyPEM, err := deterministicNodeCertificatePEM(nodeKey)
	if err != nil {
		return nil, fmt.Errorf("xray+hysteria: build TLS identity: %w", err)
	}

	tlsConfig := &xraytls.Config{
		ServerName:   sni,
		Fingerprint:  fp,
		MinVersion:   "1.3",
		MaxVersion:   "1.3",
		NextProtocol: []string{http3.NextProtoH3},
	}
	if server {
		tlsConfig.Certificate = []*xraytls.Certificate{{
			Certificate: certPEM,
			Key:         keyPEM,
			Usage:       xraytls.Certificate_ENCIPHERMENT,
		}}
	}
	return tlsConfig, nil
}

func parseHysteriaQUICParams(q url.Values) (*xrayinternet.QuicParams, error) {
	params := &xrayinternet.QuicParams{
		BbrProfile: string(xraybbr.ProfileStandard),
		UdpHop:     &xrayinternet.UdpHop{},
	}

	if v := q.Get("congestion"); v != "" {
		switch normalized := strings.ToLower(strings.TrimSpace(v)); normalized {
		case "", xraycongestion.TypeBBR, xraycongestion.TypeReno, "brutal", "force-brutal":
			params.Congestion = normalized
		default:
			return nil, fmt.Errorf("xray+hysteria: invalid 'congestion': unsupported congestion type %q", v)
		}
	}
	if v := q.Get("bbrProfile"); v != "" {
		normalized, err := xraycongestion.NormalizeBBRProfile(v)
		if err != nil {
			return nil, fmt.Errorf("xray+hysteria: invalid 'bbrProfile': %w", err)
		}
		params.BbrProfile = normalized
	}
	if v := q.Get("brutalUp"); v != "" {
		value, err := parseUint64Param("brutalUp", v)
		if err != nil {
			return nil, err
		}
		params.BrutalUp = value
	}
	if v := q.Get("brutalDown"); v != "" {
		value, err := parseUint64Param("brutalDown", v)
		if err != nil {
			return nil, err
		}
		params.BrutalDown = value
	}
	if v := q.Get("udpHopPorts"); v != "" {
		ports, err := parsePortsParam("udpHopPorts", v)
		if err != nil {
			return nil, err
		}
		params.UdpHop.Ports = ports
	}
	if v := q.Get("udpHopIntervalMin"); v != "" {
		value, err := parseInt64Param("udpHopIntervalMin", v)
		if err != nil {
			return nil, err
		}
		params.UdpHop.IntervalMin = value
	}
	if v := q.Get("udpHopIntervalMax"); v != "" {
		value, err := parseInt64Param("udpHopIntervalMax", v)
		if err != nil {
			return nil, err
		}
		params.UdpHop.IntervalMax = value
	}
	if v := q.Get("maxIdleTimeout"); v != "" {
		value, err := parseInt64Param("maxIdleTimeout", v)
		if err != nil {
			return nil, err
		}
		params.MaxIdleTimeout = value
	}
	if v := q.Get("keepAlivePeriod"); v != "" {
		value, err := parseInt64Param("keepAlivePeriod", v)
		if err != nil {
			return nil, err
		}
		params.KeepAlivePeriod = value
	}
	if v := q.Get("disablePathMTUDiscovery"); v != "" {
		value, err := parseBoolParam("disablePathMTUDiscovery", v)
		if err != nil {
			return nil, err
		}
		params.DisablePathMtuDiscovery = value
	}
	if v := q.Get("initStreamReceiveWindow"); v != "" {
		value, err := parseUint64Param("initStreamReceiveWindow", v)
		if err != nil {
			return nil, err
		}
		params.InitStreamReceiveWindow = value
	}
	if v := q.Get("maxStreamReceiveWindow"); v != "" {
		value, err := parseUint64Param("maxStreamReceiveWindow", v)
		if err != nil {
			return nil, err
		}
		params.MaxStreamReceiveWindow = value
	}
	if v := q.Get("initConnReceiveWindow"); v != "" {
		value, err := parseUint64Param("initConnReceiveWindow", v)
		if err != nil {
			return nil, err
		}
		params.InitConnReceiveWindow = value
	}
	if v := q.Get("maxConnReceiveWindow"); v != "" {
		value, err := parseUint64Param("maxConnReceiveWindow", v)
		if err != nil {
			return nil, err
		}
		params.MaxConnReceiveWindow = value
	}
	if v := q.Get("maxIncomingStreams"); v != "" {
		value, err := parseInt64Param("maxIncomingStreams", v)
		if err != nil {
			return nil, err
		}
		params.MaxIncomingStreams = value
	}
	if params.UdpHop.IntervalMax != 0 && params.UdpHop.IntervalMin != 0 && params.UdpHop.IntervalMax < params.UdpHop.IntervalMin {
		return nil, fmt.Errorf("xray+hysteria: invalid 'udpHopIntervalMax': must be >= udpHopIntervalMin")
	}
	return params, nil
}

func deterministicNodeCertificatePEM(nodeKey ed25519.PrivateKey) ([]byte, []byte, error) {
	pub := nodeKey.Public().(ed25519.PublicKey)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: hex.EncodeToString(pub),
		},
		NotBefore:             time.Unix(946684800, 0).UTC(),
		NotAfter:              time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(zeroReader{}, template, template, pub, nodeKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(nodeKey)
	if err != nil {
		return nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func dialHysteriaStream(ctx context.Context, dest xraynet.Destination, cfg *hysteriaPeerConfig) (net.Conn, error) {
	tlsProto := xraytls.ConfigFromStreamSettings(cfg.stream)
	if tlsProto == nil {
		return nil, fmt.Errorf("xray+hysteria: missing TLS config")
	}
	tlsConfig := tlsProto.GetTLSConfig()
	tlsConfig.InsecureSkipVerify = true
	tlsConfig.MinVersion = tls.VersionTLS13
	tlsConfig.MaxVersion = tls.VersionTLS13
	if cfg.sni != "" {
		tlsConfig.ServerName = cfg.sni
	}

	quicParams := cfg.stream.QuicParams
	if quicParams == nil {
		quicParams = &xrayinternet.QuicParams{
			BbrProfile: string(xraybbr.ProfileStandard),
			UdpHop:     &xrayinternet.UdpHop{},
		}
	}

	quicConfig := &quic.Config{
		InitialStreamReceiveWindow:     quicParams.InitStreamReceiveWindow,
		MaxStreamReceiveWindow:         quicParams.MaxStreamReceiveWindow,
		InitialConnectionReceiveWindow: quicParams.InitConnReceiveWindow,
		MaxConnectionReceiveWindow:     quicParams.MaxConnReceiveWindow,
		MaxIdleTimeout:                 time.Duration(quicParams.MaxIdleTimeout) * time.Second,
		KeepAlivePeriod:                time.Duration(quicParams.KeepAlivePeriod) * time.Second,
		DisablePathMTUDiscovery:        quicParams.DisablePathMtuDiscovery,
		EnableDatagrams:                true,
		MaxDatagramFrameSize:           xrayhysteria.MaxDatagramFrameSize,
		DisablePathManager:             true,
	}
	if quicConfig.InitialStreamReceiveWindow == 0 {
		quicConfig.InitialStreamReceiveWindow = 8388608
	}
	if quicConfig.MaxStreamReceiveWindow == 0 {
		quicConfig.MaxStreamReceiveWindow = 8388608
	}
	if quicConfig.InitialConnectionReceiveWindow == 0 {
		quicConfig.InitialConnectionReceiveWindow = 8388608 * 5 / 2
	}
	if quicConfig.MaxConnectionReceiveWindow == 0 {
		quicConfig.MaxConnectionReceiveWindow = 8388608 * 5 / 2
	}
	if quicConfig.MaxIdleTimeout == 0 {
		quicConfig.MaxIdleTimeout = 30 * time.Second
	}

	packetConn, remoteAddr, err := dialHysteriaPacketConn(ctx, dest)
	if err != nil {
		return nil, err
	}
	transport := &quic.Transport{Conn: packetConn}

	var quicConn *quic.Conn
	rt := &http3.Transport{
		TLSClientConfig: tlsConfig,
		QUICConfig:      quicConfig,
		Dial: func(ctx context.Context, _ string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			conn, err := transport.DialEarly(ctx, remoteAddr, tlsCfg, cfg)
			if err != nil {
				return nil, err
			}
			quicConn = conn
			return conn, nil
		},
	}

	req := &http.Request{
		Method: http.MethodPost,
		URL: &url.URL{
			Scheme: "https",
			Host:   xrayhysteria.URLHost,
			Path:   xrayhysteria.URLPath,
		},
		Header: http.Header{
			xrayhysteria.RequestHeaderAuth:   []string{cfg.password},
			xrayhysteria.CommonHeaderCCRX:    []string{strconv.FormatUint(quicParams.BrutalDown, 10)},
			xrayhysteria.CommonHeaderPadding: []string{xrayhysteria.AuthRequestPadding.String()},
		},
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		closeHysteriaClient(quicConn, transport, packetConn)
		return nil, fmt.Errorf("xray+hysteria: auth roundtrip: %w", err)
	}
	if resp.StatusCode != xrayhysteria.StatusAuthOK {
		_ = resp.Body.Close()
		closeHysteriaClient(quicConn, transport, packetConn)
		return nil, fmt.Errorf("xray+hysteria: auth failed code %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	if err := verifyHysteriaPeerTLSKey(quicConn, cfg.remote); err != nil {
		closeHysteriaClient(quicConn, transport, packetConn)
		return nil, err
	}

	down, _ := strconv.ParseUint(resp.Header.Get(xrayhysteria.CommonHeaderCCRX), 10, 64)
	switch quicParams.Congestion {
	case xraycongestion.TypeReno:
	case "", xraycongestion.TypeBBR:
		xraycongestion.UseBBR(quicConn, xraybbr.Profile(quicParams.BbrProfile))
	case "brutal":
		if quicParams.BrutalUp == 0 || down == 0 {
			xraycongestion.UseBBR(quicConn, xraybbr.Profile(quicParams.BbrProfile))
		} else {
			xraycongestion.UseBrutal(quicConn, min(quicParams.BrutalUp, down))
		}
	case "force-brutal":
		xraycongestion.UseBrutal(quicConn, quicParams.BrutalUp)
	default:
		xraycongestion.UseBBR(quicConn, xraybbr.Profile(quicParams.BbrProfile))
	}

	stream, err := quicConn.OpenStream()
	if err != nil {
		closeHysteriaClient(quicConn, transport, packetConn)
		return nil, fmt.Errorf("xray+hysteria: open stream: %w", err)
	}

	return &linkXrayHysteriaConn{
		stream:    stream,
		conn:      quicConn,
		transport: transport,
		pktConn:   packetConn,
		local:     quicConn.LocalAddr(),
		remote:    quicConn.RemoteAddr(),
		client:    true,
	}, nil
}

func dialHysteriaPacketConn(ctx context.Context, dest xraynet.Destination) (net.PacketConn, *net.UDPAddr, error) {
	conn, err := xrayinternet.DialSystem(ctx, dest, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("xray+hysteria: dial UDP: %w", err)
	}
	switch c := conn.(type) {
	case *xrayinternet.PacketConnWrapper:
		remote, ok := c.RemoteAddr().(*net.UDPAddr)
		if !ok {
			_ = c.Close()
			return nil, nil, fmt.Errorf("xray+hysteria: unexpected remote addr %T", c.RemoteAddr())
		}
		return c.PacketConn, remote, nil
	case *xraycnc.Connection:
		remote := c.RemoteAddr().(*net.TCPAddr)
		return &xrayinternet.FakePacketConn{Conn: c}, &net.UDPAddr{IP: remote.IP, Port: remote.Port}, nil
	default:
		_ = conn.Close()
		return nil, nil, fmt.Errorf("xray+hysteria: unsupported packet conn type %T", conn)
	}
}

func verifyHysteriaPeerTLSKey(conn *quic.Conn, expected ed25519.PublicKey) error {
	if conn == nil {
		return fmt.Errorf("xray+hysteria: missing QUIC connection")
	}
	peerCerts := conn.ConnectionState().TLS.PeerCertificates
	if len(peerCerts) == 0 {
		return fmt.Errorf("xray+hysteria: peer did not present a TLS certificate")
	}
	pub, ok := peerCerts[0].PublicKey.(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("xray+hysteria: peer TLS leaf key is not Ed25519")
	}
	if !pub.Equal(expected) {
		return fmt.Errorf("xray+hysteria: peer TLS leaf key does not match 'key'")
	}
	return nil
}

func closeHysteriaClient(conn *quic.Conn, transport *quic.Transport, pktConn net.PacketConn) {
	if conn != nil {
		_ = conn.CloseWithError(0x101, "")
	}
	if transport != nil {
		_ = transport.Close()
	}
	if pktConn != nil {
		_ = pktConn.Close()
	}
}

func splitHostPort(hostport string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}

func decodeEd25519PublicKey(value string) (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("hex decode: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("want %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

func parseBoolParam(name, value string) (bool, error) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("xray+hysteria: invalid '%s': %w", name, err)
	}
	return parsed, nil
}

func parseInt32Param(name, value string) (int32, error) {
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("xray+hysteria: invalid '%s': %w", name, err)
	}
	return int32(parsed), nil
}

func parseInt64Param(name, value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("xray+hysteria: invalid '%s': %w", name, err)
	}
	return parsed, nil
}

func parseUint64Param(name, value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("xray+hysteria: invalid '%s': %w", name, err)
	}
	return parsed, nil
}

func parsePortsParam(name, value string) ([]uint32, error) {
	parts := strings.Split(value, ",")
	ports := make([]uint32, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("xray+hysteria: invalid '%s': empty port entry", name)
		}
		port, err := strconv.ParseUint(part, 10, 16)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("xray+hysteria: invalid '%s': bad port %q", name, part)
		}
		ports = append(ports, uint32(port))
	}
	return ports, nil
}

func (l *linkXrayHysteriaListener) Accept() (net.Conn, error) {
	select {
	case <-l.ctx.Done():
		return nil, context.Canceled
	case conn := <-l.ch:
		if conn == nil {
			return nil, context.Canceled
		}
		return conn, nil
	}
}

func (l *linkXrayHysteriaListener) Close() error {
	l.cancel()
	if l.inner != nil {
		return l.inner.Close()
	}
	return nil
}

func (l *linkXrayHysteriaListener) Addr() net.Addr {
	return l.inner.Addr()
}

func (c *linkXrayHysteriaConn) Read(p []byte) (int, error) {
	return c.stream.Read(p)
}

func (c *linkXrayHysteriaConn) Write(p []byte) (int, error) {
	if c.client {
		c.client = false
		payload := append(quicvarint.Append(nil, xrayhysteria.FrameTypeTCPRequest), p...)
		if _, err := c.stream.Write(payload); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	return c.stream.Write(p)
}

func (c *linkXrayHysteriaConn) Close() error {
	c.stream.CancelRead(0)
	err := c.stream.Close()
	closeHysteriaClient(c.conn, c.transport, c.pktConn)
	return err
}

func (c *linkXrayHysteriaConn) LocalAddr() net.Addr {
	return c.local
}

func (c *linkXrayHysteriaConn) RemoteAddr() net.Addr {
	return c.remote
}

func (c *linkXrayHysteriaConn) SetDeadline(t time.Time) error {
	return c.stream.SetDeadline(t)
}

func (c *linkXrayHysteriaConn) SetReadDeadline(t time.Time) error {
	return c.stream.SetReadDeadline(t)
}

func (c *linkXrayHysteriaConn) SetWriteDeadline(t time.Time) error {
	return c.stream.SetWriteDeadline(t)
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
