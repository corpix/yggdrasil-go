package core

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yggdrasil-network/yggdrasil-go/src/config"
)

func newTestNode(t *testing.T) *Core {
	t.Helper()

	cfg := config.GenerateConfig()
	require_NoError(t, cfg.GenerateSelfSignedCertificate())

	node, err := New(cfg.Certificate, GetLoggerWithPrefix("", false))
	require_NoError(t, err)
	return node
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require_NoError(t, err)
	return u
}

func peerURLWithXrayHysteria(t *testing.T, addr string, key ed25519.PublicKey, password, sni string) *url.URL {
	t.Helper()

	query := url.Values{}
	query.Set("key", hex.EncodeToString(key))
	query.Set("password", password)
	if sni != "" {
		query.Set("sni", sni)
	}
	return mustParseURL(t, "xray+hysteria://"+addr+"?"+query.Encode())
}

func waitForPeerError(t *testing.T, node *Core, contains string) PeerInfo {
	t.Helper()

	return waitForPersistentPeer(t, node, func(peer PeerInfo) bool {
		return !peer.Up && peer.LastError != nil && strings.Contains(peer.LastError.Error(), contains)
	})
}

func TestXrayHysteriaListenerRequiresPassword(t *testing.T) {
	node := newTestNode(t)
	defer node.Stop()

	_, err := node.Listen(mustParseURL(t, "xray+hysteria://127.0.0.1:0"), "")
	if err == nil || !strings.Contains(err.Error(), "missing required param 'password'") {
		t.Fatalf("expected missing password error, got %v", err)
	}
}

func TestXrayHysteriaPeerRequiresPassword(t *testing.T) {
	node := newTestNode(t)
	defer node.Stop()

	u := mustParseURL(t, "xray+hysteria://127.0.0.1:1?key="+hex.EncodeToString(node.PublicKey()))
	_, err := node.links.connect(context.Background(), u, linkInfo{}, linkOptions{})
	if err == nil || !strings.Contains(err.Error(), "missing required param 'password'") {
		t.Fatalf("expected missing password error, got %v", err)
	}
}

func TestXrayHysteriaPeerRequiresKey(t *testing.T) {
	node := newTestNode(t)
	defer node.Stop()

	u := mustParseURL(t, "xray+hysteria://127.0.0.1:1?password=secret")
	_, err := node.links.connect(context.Background(), u, linkInfo{}, linkOptions{password: []byte("secret")})
	if err == nil || !strings.Contains(err.Error(), "missing required param 'key'") {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

func TestXrayHysteriaRejectsMalformedParams(t *testing.T) {
	node := newTestNode(t)
	defer node.Stop()

	u := mustParseURL(t, "xray+hysteria://127.0.0.1:1?key="+hex.EncodeToString(node.PublicKey())+"&password=secret&udpHopPorts=bad")
	_, err := node.links.connect(context.Background(), u, linkInfo{}, linkOptions{password: []byte("secret")})
	if err == nil || !strings.Contains(err.Error(), "invalid 'udpHopPorts'") {
		t.Fatalf("expected malformed udpHopPorts error, got %v", err)
	}
}

func TestXrayHysteriaAcceptsBrutalCongestionParams(t *testing.T) {
	node := newTestNode(t)
	defer node.Stop()

	u := mustParseURL(t, "xray+hysteria://127.0.0.1:1?key="+hex.EncodeToString(node.PublicKey())+"&password=secret&congestion=brutal&brutalUp=1000000&brutalDown=1000000")
	_, err := node.links.xrayHysteria.parsePeerConfig(u)
	require_NoError(t, err)
}

func TestXrayHysteriaConnectsNodes(t *testing.T) {
	nodeA := newTestNode(t)
	defer nodeA.Stop()
	nodeB := newTestNode(t)
	defer nodeB.Stop()

	listenerURL := mustParseURL(t, "xray+hysteria://127.0.0.1:0?password=shared-secret&sni=example.com")
	listener, err := nodeA.Listen(listenerURL, "")
	require_NoError(t, err)

	peerURL := peerURLWithXrayHysteria(t, listener.Addr().String(), nodeA.PublicKey(), "shared-secret", "example.com")
	require_NoError(t, nodeB.AddPeer(peerURL, ""))
	if !WaitConnected(nodeA, nodeB) {
		t.Fatal("nodes did not connect")
	}

	peer := waitForPersistentPeer(t, nodeB, func(peer PeerInfo) bool {
		return peer.Up && peer.LastError == nil
	})
	require_Equal(t, peer.SNI, "example.com")
}

func TestXrayHysteriaWrongPasswordFails(t *testing.T) {
	nodeA := newTestNode(t)
	defer nodeA.Stop()
	nodeB := newTestNode(t)
	defer nodeB.Stop()

	listenerURL := mustParseURL(t, "xray+hysteria://127.0.0.1:0?password=shared-secret&sni=example.com")
	listener, err := nodeA.Listen(listenerURL, "")
	require_NoError(t, err)

	peerURL := peerURLWithXrayHysteria(t, listener.Addr().String(), nodeA.PublicKey(), "wrong-secret", "example.com")
	require_NoError(t, nodeB.AddPeer(peerURL, ""))

	peer := waitForPeerError(t, nodeB, "auth failed")
	require_True(t, !peer.Up)
}

func TestXrayHysteriaTLSLeafKeyMismatchFails(t *testing.T) {
	nodeA := newTestNode(t)
	defer nodeA.Stop()
	nodeB := newTestNode(t)
	defer nodeB.Stop()
	nodeC := newTestNode(t)
	defer nodeC.Stop()

	listenerURL := mustParseURL(t, "xray+hysteria://127.0.0.1:0?password=shared-secret&sni=example.com")
	listener, err := nodeA.Listen(listenerURL, "")
	require_NoError(t, err)

	peerURL := peerURLWithXrayHysteria(t, listener.Addr().String(), nodeC.PublicKey(), "shared-secret", "example.com")
	require_NoError(t, nodeB.AddPeer(peerURL, ""))

	peer := waitForPeerError(t, nodeB, "TLS leaf key")
	require_True(t, !peer.Up)
}

func TestXrayHysteriaTransportAuthAndYggPasswordLayering(t *testing.T) {
	nodeA := newTestNode(t)
	defer nodeA.Stop()
	nodeB := newTestNode(t)
	defer nodeB.Stop()

	listenerURL := mustParseURL(t, "xray+hysteria://127.0.0.1:0?password=transport-secret&sni=example.com")
	listener, err := nodeA.Listen(listenerURL, "")
	require_NoError(t, err)

	peerURL := peerURLWithXrayHysteria(t, listener.Addr().String(), nodeA.PublicKey(), "transport-secret", "example.com")

	conn, err := nodeB.links.connect(context.Background(), peerURL, linkInfo{}, linkOptions{password: []byte("wrong-ygg-secret")})
	require_NoError(t, err)
	defer conn.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- nodeB.links.handler(&link{linkProto: strings.ToUpper(peerURL.Scheme)}, linkTypePersistent, linkOptions{
			password: []byte("wrong-ygg-secret"),
			tlsSNI:   "example.com",
		}, conn, nil, false)
	}()

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "password") {
			t.Fatalf("expected Ygg handshake password failure, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Ygg handshake failure")
	}
}
