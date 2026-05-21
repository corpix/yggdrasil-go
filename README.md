# Yggdrasil

> This repository is an attempt on patching original Yggdrasil with some
> DPI resistance.

## Introduction

Yggdrasil is an early-stage implementation of a fully end-to-end encrypted IPv6
network. It is lightweight, self-arranging, supported on multiple platforms and
allows pretty much any IPv6-capable application to communicate securely with
other Yggdrasil nodes. Yggdrasil does not require you to have IPv6 Internet
connectivity, it also works over IPv4.

## Features

* End-to-end encrypted IPv6 mesh routing in the `200::/7` range, with a TUN
  interface exposed to the host so any IPv6-capable application can use it.
* Multicast peer discovery on local networks, optionally password-protected
  per interface.
* A range of peer transports:
  * `xray+reality://` X-Ray REALITY, which makes the peering look like a real
    TLS handshake to an arbitrary destination (`?sni=`, `?dest=host:443`).
  * `xray+hysteria://` X-Ray Hysteria over QUIC with a shared password and
    SNI, for high-loss or DPI-heavy environments.
  * `tls://` TCP wrapped in TLS, with an optional `?sni=` to set the SNI
    presented to middleboxes. `OutboundSNIList` provides sticky per-peer
    SNI rotation when a peer does not specify one explicitly.
  * `quic://` QUIC, useful on lossy links.
  * `ws://` and `wss://` WebSocket, with a configurable `origin=` query
    option (repeatable, `origin=*` to disable verification) so browser
    clients can dial a public peer.
  * `socks://` and `sockstls://` outbound peering via a SOCKS proxy.
  * `tcp://` plain TCP.
  * `unix://` Unix domain sockets, including relative paths.
* Private networks via a shared `Community` string. The string is never sent
  in plaintext, it is mixed into handshake key material so peers without the
  matching community cannot complete the handshake. `CommunityMode` chooses
  between `strict` (both sides must support communities) and `soft` (allow
  outbound to legacy peers, reject inbound mismatches). The private network
  IPv6 prefix is configurable.
* Built-in Prometheus exporter (`PrometheusEnabled`, `PrometheusListen`,
  default `127.0.0.1:9756`) for scraping peer, link and routing metrics.
* Optional web UI (`WebUI.Enable`, `WebUI.Host`, `WebUI.Port`) for managing
  the node from a browser.
* `yggdrasilctl` for live introspection and control over the admin socket,
  with `-verbose` to show the last error per peer and `-state up|down` to
  filter `getPeers` by link state. 
* `-normaliseconf` creates the target config file if it does not yet exist,
  which makes it usable as a one-shot config migrator.
* In-tree `ironwood` routing layer with a disconnect penalty in peer-cost
  calculation, which produces more stable routes when links flap.

Example configurations live in the repo root: `test.sni.conf`,
`test.webui.conf`, `test.xray-reality.conf`, `test.xray-hysteria.conf`.

## Building

If you would rather build from source than install a pre-built package:

1. Install [Go](https://golang.org). Version 1.26 or later is required.
2. Clone this repository.
3. Run `just build`.

`just build` accepts a handful of switches:

- `debug=1`
- `race=1`
- `pie=1`
- `upx=1`
- `output=...`
- `ldflags=...`
- `gcflags=...`

Native package builds:

- `just build-debian`
- `just build-macos`
- `just build-windows`

To cross-compile, set `GOOS` and `GOARCH` as usual, e.g.
`GOOS=windows just build` or `GOOS=linux GOARCH=mipsle just build`.

## Running

### Generate configuration

Generate an HJSON file (human-friendly, with comments):

```
./yggdrasil -genconf > /path/to/yggdrasil.conf
```

Or a plain JSON file (easier to manipulate programmatically):

```
./yggdrasil -genconf -json > /path/to/yggdrasil.conf
```

Edit `yggdrasil.conf` to add or remove peers, change listen or multicast
addresses, set up a `Community`, enable Prometheus or the web UI, and so on.

### Run Yggdrasil

With a static config:

```
./yggdrasil -useconffile /path/to/yggdrasil.conf
```

Or in auto-configuration mode, which uses sane defaults and fresh random keys
on every startup:

```
./yggdrasil -autoconf
```

You will typically need to run as root or under `sudo` so yggdrasil can create
the TUN adapter. On Linux you can avoid that by giving the binary the
`CAP_NET_ADMIN` capability instead.

## License

This code is released under the terms of the LGPLv3, with an added exception
shamelessly borrowed from [godeb](https://github.com/niemeyer/godeb). Under
certain circumstances that exception permits distribution of binaries
statically or dynamically linked with this code without requiring distribution
of Minimal Corresponding Source or Minimal Application Code. See
[LICENSE](LICENSE) for the details.
