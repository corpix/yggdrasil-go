// REALITY transport for Yggdrasil over raw TCP.
//
// Listener URI: xray+reality://0.0.0.0:443?sni=<domain>&dest=<domain:port>[&sid=<hex>]
// Peer URI:     xray+reality://host:port?key=<ygg-pubkey-hex>&sni=<domain>[&fp=chrome][&sid=<hex>]
//
// key= in the peer URI is the remote node's Yggdrasil public key (Ed25519, hex).
// It is converted to the REALITY X25519 public key internally via the Edwards->Montgomery
// birational map, so no separate pbk= value needs to be exchanged.
//
// The listener's REALITY private key is derived deterministically from the node's
// Ed25519 private key seed, so no keyFile is needed.
//
// Additional standard Yggdrasil query params (key=, password=, priority=, maxbackoff=)
// are handled by the core link machinery and remain unchanged.

package core

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"time"

	goreality "github.com/xtls/reality"
	xraynet "github.com/xtls/xray-core/common/net"
	xrayreality "github.com/xtls/xray-core/transport/internet/reality"
	"filippo.io/edwards25519"
)

type linkXrayReality struct {
	*links
}

func (l *links) newLinkXrayReality() *linkXrayReality {
	return &linkXrayReality{links: l}
}

// dial handles outbound xray+reality:// connections.
// Required params: key (remote Yggdrasil Ed25519 public key, hex), sni (or serverName).
// Optional params: fp (fingerprint, default chrome), sid (short ID, hex).
func (l *linkXrayReality) dial(ctx context.Context, u *url.URL, info linkInfo, options linkOptions) (net.Conn, error) {
	q := u.Query()

	keyHex := q.Get("key")
	if keyHex == "" {
		return nil, fmt.Errorf("xray+reality: missing required param 'key'")
	}
	edPubBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("xray+reality: invalid 'key' (hex): %w", err)
	}
	pubKeyBytes, err := realityPubKeyFromNodeKey(edPubBytes)
	if err != nil {
		return nil, fmt.Errorf("xray+reality: convert 'key' to REALITY pubkey: %w", err)
	}

	sni := q.Get("sni")
	if sni == "" {
		sni = q.Get("serverName")
	}
	if sni == "" {
		return nil, fmt.Errorf("xray+reality: missing required param 'sni' or 'serverName'")
	}

	fp := q.Get("fp")
	if fp == "" {
		fp = "chrome"
	}

	var shortID []byte
	if sid := q.Get("sid"); sid != "" {
		shortID, err = hex.DecodeString(sid)
		if err != nil {
			return nil, fmt.Errorf("xray+reality: invalid 'sid' (hex): %w", err)
		}
	}

	cfg := &xrayreality.Config{
		ServerName:  sni,
		Fingerprint: fp,
		PublicKey:   pubKeyBytes,
		ShortId:     shortID,
		// SpiderX/SpiderY: one GET to "/" then 1-2 s return delay so a
		// MITM/active-probe connection is never fingerprintable as
		// "TLS handshake → immediate close".
		SpiderX: "/",
		SpiderY: []int64{0, 0, 0, 0, 0, 0, 0, 0, 1000, 2000},
	}

	return l.findSuitableIP(u, func(hostname string, ip net.IP, port int) (net.Conn, error) {
		addr := &net.TCPAddr{IP: ip, Port: port}
		dialer, err := l.tcp.dialerFor(addr, info.sintf)
		if err != nil {
			return nil, err
		}
		rawConn, err := dialer.DialContext(ctx, "tcp", addr.String())
		if err != nil {
			return nil, err
		}
		conn, err := xrayreality.UClient(rawConn, cfg, ctx, xraynet.DestinationFromAddr(addr))
		if err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("xray+reality: handshake: %w", err)
		}
		return conn, nil
	})
}

// listen handles inbound xray+reality:// listeners.
// Required params: sni (or serverName), dest.
// Optional params: sid (hex).
// The REALITY private key is derived from the node's Ed25519 private key seed,
// so it is stable across restarts without any keyFile.
func (l *linkXrayReality) listen(ctx context.Context, u *url.URL, sintf string) (net.Listener, error) {
	q := u.Query()

	sni := q.Get("sni")
	if sni == "" {
		sni = q.Get("serverName")
	}
	if sni == "" {
		return nil, fmt.Errorf("xray+reality: missing required param 'sni' or 'serverName'")
	}

	dest := q.Get("dest")
	if dest == "" {
		return nil, fmt.Errorf("xray+reality: missing required param 'dest' (REALITY fallback address, e.g. www.example.com:443)")
	}

	privKeyBytes := realityPrivKeyFromNodeKey(l.core.secret.Seed())

	shortIDs := map[[8]byte]bool{{}: true}
	if sid := q.Get("sid"); sid != "" {
		b, err := hex.DecodeString(sid)
		if err != nil {
			return nil, fmt.Errorf("xray+reality: invalid 'sid' (hex): %w", err)
		}
		var id [8]byte
		copy(id[:], b)
		shortIDs[id] = true
	}

	realityConfig := &goreality.Config{
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		Dest:                   dest,
		Type:                   "tcp",
		PrivateKey:             privKeyBytes,
		ServerNames:            map[string]bool{sni: true},
		ShortIds:               shortIDs,
		MaxTimeDiff:            3 * time.Minute,
		SessionTicketsDisabled: true,
	}

	hostport := u.Host
	if sintf != "" {
		host, port, splitErr := net.SplitHostPort(hostport)
		if splitErr == nil {
			hostport = fmt.Sprintf("[%s%%%s]:%s", host, sintf, port)
		}
	}
	inner, err := (&net.ListenConfig{KeepAlive: -1}).Listen(ctx, "tcp", hostport)
	if err != nil {
		return nil, err
	}
	return goreality.NewListener(inner, realityConfig), nil
}

// realityPrivKeyFromNodeKey derives the REALITY X25519 private key from the
// Yggdrasil node's Ed25519 private key seed. Derivation mirrors Ed25519's own
// internal scalar derivation: SHA-512(seed)[0:32] with X25519 clamping applied.
// The result is stable across restarts for the same node key.
func realityPrivKeyFromNodeKey(seed []byte) []byte {
	h := sha512.Sum512(seed)
	priv := make([]byte, 32)
	copy(priv, h[:32])
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	return priv
}

// realityPubKeyFromNodeKey converts a Yggdrasil Ed25519 public key to the
// REALITY X25519 public key via filippo.io/edwards25519 BytesMontgomery.
func realityPubKeyFromNodeKey(edPub []byte) ([]byte, error) {
	p, err := edwards25519.NewIdentityPoint().SetBytes(edPub)
	if err != nil {
		return nil, err
	}
	return p.BytesMontgomery(), nil
}
