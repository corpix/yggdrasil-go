module github.com/yggdrasil-network/yggdrasil-go

go 1.26

require (
	filippo.io/edwards25519 v1.2.0
	github.com/Arceliar/ironwood v0.0.0-00010101000000-000000000000
	github.com/Arceliar/phony v0.0.0-20220903101357-530938a4b13d
	github.com/apernet/quic-go v0.59.1-0.20260425001925-6c6cc9bcb716
	github.com/cheggaaa/pb/v3 v3.1.7
	github.com/coder/websocket v1.8.14
	github.com/gologme/log v1.3.0
	github.com/hashicorp/go-syslog v1.0.0
	github.com/hjson/hjson-go/v4 v4.6.0
	github.com/kardianos/minwinsvc v1.0.2
	github.com/prometheus/client_golang v1.23.2
	github.com/quic-go/quic-go v0.59.0
	github.com/vishvananda/netlink v1.3.1
	github.com/wlynxg/anet v0.0.5
	github.com/xtls/reality v0.0.0-20260322125925-9234c772ba8f
	github.com/xtls/xray-core v1.260327.1-0.20260507141325-1dbafe629a09
	golang.org/x/crypto v0.50.0
	golang.org/x/net v0.53.0
	golang.org/x/sys v0.43.0
	golang.org/x/text v0.36.0
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2
	golang.zx2c4.com/wireguard v0.0.0-20250521234502-f333402bd9cb
	golang.zx2c4.com/wireguard/windows v1.0.1
)

require (
	github.com/andybalholm/brotli v1.0.6 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.24.4 // indirect
	github.com/bits-and-blooms/bloom/v3 v3.7.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/clipperhouse/displaywidth v0.10.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/juju/ratelimit v1.0.2 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/miekg/dns v1.1.72 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/olekukonko/cat v0.0.0-20250911104152-50322a0618f6 // indirect
	github.com/olekukonko/errors v1.2.0 // indirect
	github.com/olekukonko/ll v0.1.6 // indirect
	github.com/pires/go-proxyproto v0.12.0 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/refraction-networking/utls v1.8.3-0.20260301010127-aa6edf4b11af // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	go4.org/netipx v0.0.0-20231129151722-fdeea329fbba // indirect
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
	golang.org/x/mod v0.34.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/tools v0.43.0 // indirect
	google.golang.org/grpc v1.81.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/Arceliar/ironwood => ./ironwood

require (
	github.com/VividCortex/ewma v1.2.0 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.20 // indirect
	github.com/olekukonko/tablewriter v1.1.3
	github.com/vishvananda/netns v0.0.5 // indirect
	suah.dev/protect v1.2.4
)
