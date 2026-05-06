package admin

import (
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/gologme/log"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

func TestGetPeersIncludesCommunityStatus(t *testing.T) {
	cfgA, cfgB := config.GenerateConfig(), config.GenerateConfig()
	if err := cfgA.GenerateSelfSignedCertificate(); err != nil {
		t.Fatal(err)
	}
	if err := cfgB.GenerateSelfSignedCertificate(); err != nil {
		t.Fatal(err)
	}

	logger := log.New(io.Discard, "", 0)

	nodeA, err := core.New(cfgA.Certificate, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer nodeA.Stop()

	nodeB, err := core.New(cfgB.Certificate, logger,
		core.Community([]byte("community")),
		core.CommunityMode(core.CommunityModeSoft),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer nodeB.Stop()

	listenURL, err := url.Parse("tcp://localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := nodeA.Listen(listenURL, "")
	if err != nil {
		t.Fatal(err)
	}

	peerURL, err := url.Parse("tcp://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := nodeB.AddPeer(peerURL, ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(nodeA.GetTree()) > 1 && len(nodeB.GetTree()) > 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(nodeA.GetTree()) <= 1 || len(nodeB.GetTree()) <= 1 {
		t.Fatal("nodes did not connect")
	}

	adminSocket := &AdminSocket{core: nodeB}
	resp := &GetPeersResponse{}
	if err := adminSocket.getPeersHandler(&GetPeersRequest{}, resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Peers) != 1 {
		t.Fatalf("unexpected peer count: %d", len(resp.Peers))
	}
	if resp.Peers[0].CommunityStatus != "legacy" {
		t.Fatalf("unexpected community status: %q", resp.Peers[0].CommunityStatus)
	}
}
