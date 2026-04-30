package metrics

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/multicast"
	"github.com/yggdrasil-network/yggdrasil-go/src/tun"
	"github.com/yggdrasil-network/yggdrasil-go/src/version"
)

const ns = "yggdrasil"

var (
	peerLabels    = []string{"self", "peer", "remote", "ip", "direction"}
	sessionLabels = []string{"self", "peer", "ip"}
)

type Metrics struct {
	core      *core.Core
	tun       *tun.TunAdapter
	multicast *multicast.Multicast
	log       core.Logger
	server    *http.Server

	buildInfo       *prometheus.Desc
	routingEntries  *prometheus.Desc
	peersTotal      *prometheus.Desc
	peersUpTotal    *prometheus.Desc
	peerUp          *prometheus.Desc
	peerBytesSent   *prometheus.Desc
	peerBytesRecv   *prometheus.Desc
	peerTXRate      *prometheus.Desc
	peerRXRate      *prometheus.Desc
	peerUptime      *prometheus.Desc
	peerLatency     *prometheus.Desc
	peerPriority    *prometheus.Desc
	peerCost        *prometheus.Desc
	peerLastErrTime *prometheus.Desc
	sessionsTotal   *prometheus.Desc
	sessionTXBytes  *prometheus.Desc
	sessionRXBytes  *prometheus.Desc
	sessionUptime   *prometheus.Desc
	treeTotal       *prometheus.Desc
	pathsTotal      *prometheus.Desc
	tunEnabled      *prometheus.Desc
	tunMTU          *prometheus.Desc
	multicastTotal  *prometheus.Desc
	multicastIfUp   *prometheus.Desc
}

func New(c *core.Core, t *tun.TunAdapter, m *multicast.Multicast, log core.Logger, listenAddr string) (*Metrics, error) {
	mx := &Metrics{
		core:      c,
		tun:       t,
		multicast: m,
		log:       log,
	}

	desc := func(name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(ns+"_"+name, help, labels, nil)
	}

	mx.buildInfo = desc("build_info", "Yggdrasil build information.", []string{"version", "build_name", "public_key", "address"})
	mx.routingEntries = desc("routing_entries", "Number of entries in the routing table.", nil)
	mx.peersTotal = desc("peers_total", "Total number of configured peers.", nil)
	mx.peersUpTotal = desc("peers_up_total", "Number of peers currently connected.", nil)
	mx.peerUp = desc("peer_up", "Whether the peer connection is up (1) or down (0).", peerLabels)
	mx.peerBytesSent = desc("peer_bytes_sent_total", "Total bytes sent to the peer.", peerLabels)
	mx.peerBytesRecv = desc("peer_bytes_received_total", "Total bytes received from the peer.", peerLabels)
	mx.peerTXRate = desc("peer_tx_rate_bytes", "Current transmit rate to the peer in bytes per second.", peerLabels)
	mx.peerRXRate = desc("peer_rx_rate_bytes", "Current receive rate from the peer in bytes per second.", peerLabels)
	mx.peerUptime = desc("peer_uptime_seconds", "Duration in seconds the peer connection has been up.", peerLabels)
	mx.peerLatency = desc("peer_latency_microseconds", "Round-trip latency to the peer in microseconds.", peerLabels)
	mx.peerPriority = desc("peer_priority", "Priority of the peer connection.", peerLabels)
	mx.peerCost = desc("peer_cost", "Routing cost of the peer connection.", peerLabels)
	mx.peerLastErrTime = desc("peer_last_error_seconds_ago", "Seconds since the last peer error (0 if no error has occurred).", peerLabels)
	mx.sessionsTotal = desc("sessions_total", "Number of active traffic sessions.", nil)
	mx.sessionTXBytes = desc("session_bytes_sent_total", "Total bytes sent in the traffic session.", sessionLabels)
	mx.sessionRXBytes = desc("session_bytes_received_total", "Total bytes received in the traffic session.", sessionLabels)
	mx.sessionUptime = desc("session_uptime_seconds", "Duration in seconds the traffic session has been active.", sessionLabels)
	mx.treeTotal = desc("tree_entries_total", "Number of entries in the spanning tree.", nil)
	mx.pathsTotal = desc("paths_total", "Number of established forwarding paths through this node.", nil)
	mx.tunEnabled = desc("tun_enabled", "Whether the TUN interface is enabled (1) or not (0).", []string{"name"})
	mx.tunMTU = desc("tun_mtu_bytes", "MTU of the TUN interface in bytes.", []string{"name"})
	mx.multicastTotal = desc("multicast_interfaces_total", "Number of multicast-enabled interfaces.", nil)
	mx.multicastIfUp = desc("multicast_interface_up", "Whether the multicast interface has an active listener (1) or not (0).", []string{"name", "beacon", "listen"})

	reg := prometheus.NewRegistry()
	if err := reg.Register(mx); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mx.server = &http.Server{Addr: listenAddr, Handler: mux}

	go func() {
		if err := mx.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("Prometheus metrics server: %v\n", err)
		}
	}()

	log.Infof("Prometheus metrics listening on http://%s/metrics\n", listenAddr)
	return mx, nil
}

func (mx *Metrics) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return mx.server.Shutdown(ctx)
}

func (mx *Metrics) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range mx.descs() {
		ch <- d
	}
}

func (mx *Metrics) descs() []*prometheus.Desc {
	return []*prometheus.Desc{
		mx.buildInfo, mx.routingEntries, mx.peersTotal, mx.peersUpTotal,
		mx.peerUp, mx.peerBytesSent, mx.peerBytesRecv, mx.peerTXRate, mx.peerRXRate,
		mx.peerUptime, mx.peerLatency, mx.peerPriority, mx.peerCost, mx.peerLastErrTime,
		mx.sessionsTotal, mx.sessionTXBytes, mx.sessionRXBytes, mx.sessionUptime,
		mx.treeTotal, mx.pathsTotal,
		mx.tunEnabled, mx.tunMTU,
		mx.multicastTotal, mx.multicastIfUp,
	}
}

func (mx *Metrics) Collect(ch chan<- prometheus.Metric) {
	selfKey := hex.EncodeToString(mx.core.PublicKey())
	selfAddr := keyToAddr(mx.core.PublicKey())

	self := mx.core.GetSelf()
	ch <- prometheus.MustNewConstMetric(mx.buildInfo, prometheus.GaugeValue, 1,
		version.BuildVersion(), version.BuildName(), selfKey, selfAddr)
	ch <- prometheus.MustNewConstMetric(mx.routingEntries, prometheus.GaugeValue,
		float64(self.RoutingEntries))

	peers := mx.core.GetPeers()
	var upCount float64
	for _, p := range peers {
		if p.Up {
			upCount++
		}
	}
	ch <- prometheus.MustNewConstMetric(mx.peersTotal, prometheus.GaugeValue, float64(len(peers)))
	ch <- prometheus.MustNewConstMetric(mx.peersUpTotal, prometheus.GaugeValue, upCount)

	for _, p := range peers {
		dir := "out"
		if p.Inbound {
			dir = "in"
		}
		upVal := boolGauge(p.Up)
		labels := []string{selfKey, keyToHex(p.Key), p.URI, keyToAddr(p.Key), dir}

		ch <- prometheus.MustNewConstMetric(mx.peerUp, prometheus.GaugeValue, upVal, labels...)
		ch <- prometheus.MustNewConstMetric(mx.peerBytesSent, prometheus.CounterValue, float64(p.TXBytes), labels...)
		ch <- prometheus.MustNewConstMetric(mx.peerBytesRecv, prometheus.CounterValue, float64(p.RXBytes), labels...)
		ch <- prometheus.MustNewConstMetric(mx.peerTXRate, prometheus.GaugeValue, float64(p.TXRate), labels...)
		ch <- prometheus.MustNewConstMetric(mx.peerRXRate, prometheus.GaugeValue, float64(p.RXRate), labels...)
		ch <- prometheus.MustNewConstMetric(mx.peerUptime, prometheus.GaugeValue, p.Uptime.Seconds(), labels...)
		ch <- prometheus.MustNewConstMetric(mx.peerLatency, prometheus.GaugeValue, float64(p.Latency.Microseconds()), labels...)
		ch <- prometheus.MustNewConstMetric(mx.peerPriority, prometheus.GaugeValue, float64(p.Priority), labels...)
		ch <- prometheus.MustNewConstMetric(mx.peerCost, prometheus.GaugeValue, float64(p.Cost), labels...)

		var lastErr float64
		if !p.LastErrorTime.IsZero() {
			lastErr = time.Since(p.LastErrorTime).Seconds()
		}
		ch <- prometheus.MustNewConstMetric(mx.peerLastErrTime, prometheus.GaugeValue, lastErr, labels...)
	}

	sessions := mx.core.GetSessions()
	ch <- prometheus.MustNewConstMetric(mx.sessionsTotal, prometheus.GaugeValue, float64(len(sessions)))
	for _, s := range sessions {
		labels := []string{selfKey, keyToHex(s.Key), keyToAddr(s.Key)}
		ch <- prometheus.MustNewConstMetric(mx.sessionTXBytes, prometheus.CounterValue, float64(s.TXBytes), labels...)
		ch <- prometheus.MustNewConstMetric(mx.sessionRXBytes, prometheus.CounterValue, float64(s.RXBytes), labels...)
		ch <- prometheus.MustNewConstMetric(mx.sessionUptime, prometheus.GaugeValue, s.Uptime.Seconds(), labels...)
	}

	ch <- prometheus.MustNewConstMetric(mx.treeTotal, prometheus.GaugeValue, float64(len(mx.core.GetTree())))
	ch <- prometheus.MustNewConstMetric(mx.pathsTotal, prometheus.GaugeValue, float64(len(mx.core.GetPaths())))

	if mx.tun != nil {
		info := mx.tun.GetTUN()
		name := info.Name
		if name == "" {
			name = "none"
		}
		ch <- prometheus.MustNewConstMetric(mx.tunEnabled, prometheus.GaugeValue, boolGauge(info.Enabled), name)
		if info.Enabled {
			ch <- prometheus.MustNewConstMetric(mx.tunMTU, prometheus.GaugeValue, float64(info.MTU), name)
		}
	}

	if mx.multicast != nil {
		info := mx.multicast.GetMulticastInterfaces()
		ch <- prometheus.MustNewConstMetric(mx.multicastTotal, prometheus.GaugeValue, float64(len(info.Interfaces)))
		for _, iface := range info.Interfaces {
			up := boolGauge(iface.Address != "-")
			ch <- prometheus.MustNewConstMetric(mx.multicastIfUp, prometheus.GaugeValue, up,
				iface.Name, strconv.FormatBool(iface.Beacon), strconv.FormatBool(iface.Listen))
		}
	}
}

func keyToHex(key ed25519.PublicKey) string {
	if len(key) == 0 {
		return ""
	}
	return hex.EncodeToString(key)
}

func keyToAddr(key ed25519.PublicKey) string {
	if len(key) == 0 {
		return ""
	}
	addr := address.AddrForKey(key)
	if addr == nil {
		return ""
	}
	return net.IP(addr[:]).String()
}

func boolGauge(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
