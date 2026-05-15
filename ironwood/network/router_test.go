package network

import (
	"net"
	"testing"
	"time"
)

type testProtocolConn struct {
	net.Conn
	protocol string
}

func (c testProtocolConn) Protocol() string {
	return c.protocol
}

func TestPeerProtocolFromConn(t *testing.T) {
	if got := peerProtocolFromConn(nil); got != "UNKNOWN" {
		t.Fatalf("unexpected nil protocol: %q", got)
	}

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	if got := peerProtocolFromConn(c1); got != "UNKNOWN" {
		t.Fatalf("unexpected plain conn protocol: %q", got)
	}

	if got := peerProtocolFromConn(testProtocolConn{Conn: c2, protocol: "QUIC"}); got != "QUIC" {
		t.Fatalf("unexpected protocol conn protocol: %q", got)
	}
}

func TestRouterPenaltyIsScopedByProtocol(t *testing.T) {
	var key publicKey
	copy(key[:], []byte("peer-key-1"))

	r := &router{
		penalties: map[routerPenaltyKey]routerPenaltyEntry{
			{key: key, protocol: "TCP"}: {
				penaltyMs:  100,
				recordedAt: time.Now(),
			},
			{key: key, protocol: "QUIC"}: {
				penaltyMs:  400,
				recordedAt: time.Now(),
			},
		},
	}

	if got := r._getDecayedPenalty(routerPenaltyKey{key: key, protocol: "TCP"}); got < 90 || got > 100 {
		t.Fatalf("unexpected TCP penalty: %d", got)
	}
	if got := r._getDecayedPenalty(routerPenaltyKey{key: key, protocol: "QUIC"}); got < 390 || got > 400 {
		t.Fatalf("unexpected QUIC penalty: %d", got)
	}
}

func TestRouterRecordDisconnectUsesKeyAndProtocol(t *testing.T) {
	var key publicKey
	copy(key[:], []byte("peer-key-2"))

	p := &peer{
		key:      key,
		protocol: "WSS",
	}
	r := &router{
		lags:      map[*peer]time.Duration{p: 250 * time.Millisecond},
		penalties: make(map[routerPenaltyKey]routerPenaltyEntry),
	}

	r._recordDisconnect(p)

	if got := r._getDecayedPenalty(routerPenaltyKey{key: key, protocol: "WSS"}); got < 240 || got > 250 {
		t.Fatalf("unexpected WSS penalty: %d", got)
	}
	if got := r._getDecayedPenalty(routerPenaltyKey{key: key, protocol: "TCP"}); got != 0 {
		t.Fatalf("unexpected TCP penalty bleed: %d", got)
	}
}

func TestRouterPenalizeMissedResponsesAccumulatesPenalty(t *testing.T) {
	var key publicKey
	copy(key[:], []byte("peer-key-3"))

	p := &peer{
		key:      key,
		protocol: "TLS",
	}
	r := &router{
		peers: map[publicKey]map[*peer]struct{}{
			key: {p: {}},
		},
		requests:  map[publicKey]routerSigReq{key: {}},
		responded: make(map[*peer]struct{}),
		lags:      map[*peer]time.Duration{p: 400 * time.Millisecond},
		penalties: make(map[routerPenaltyKey]routerPenaltyEntry),
	}

	r._penalizeMissedResponses()

	if got := r.lags[p]; got != 450*time.Millisecond {
		t.Fatalf("unexpected lag after missed response: %v", got)
	}
	if got := r._getDecayedPenalty(routerPenaltyKey{key: key, protocol: "TLS"}); got < minLossPenalty.Milliseconds() {
		t.Fatalf("expected durable loss penalty, got %d", got)
	}
}
