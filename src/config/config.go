/*
The config package contains structures related to the configuration of an
Yggdrasil node.

The configuration contains, amongst other things, encryption keys which are used
to derive a node's identity, information about peerings and node information
that is shared with the network. There are also some module-specific options
related to TUN, multicast and the admin socket.

In order for a node to maintain the same identity across restarts, you should
persist the configuration onto the filesystem or into some configuration storage
so that the encryption keys (and therefore the node ID) do not change.

Note that Yggdrasil will automatically populate sane defaults for any
configuration option that is not provided.
*/
package config

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hjson/hjson-go/v4"
	"golang.org/x/text/encoding/unicode"
)

// NodeConfig is the main configuration structure, containing configuration
// options that are necessary for an Yggdrasil node to run. You will need to
// supply one of these structs to the Yggdrasil core when starting a node.
type NodeConfig struct {
	PrivateKey          KeyBytes                   `json:",omitempty" comment:"Your private key. DO NOT share this with anyone!"`
	PrivateKeyPath      string                     `json:",omitempty" comment:"The path to your private key file in PEM format."`
	Certificate         *tls.Certificate           `json:"-"`
	Peers               []string                   `comment:"List of outbound peer connection strings (e.g. tls://a.b.c.d:e or\nsocks://a.b.c.d:e/f.g.h.i:j). Connection strings can contain options,\nsee https://yggdrasil-network.github.io/configurationref.html#peers.\nYggdrasil has no concept of bootstrap nodes - all network traffic\nwill transit peer connections. Therefore make sure to only peer with\nnearby nodes that have good connectivity and low latency. Avoid adding\npeers to this list from distant countries as this will worsen your\nnode's connectivity and performance considerably."`
	OutboundSNIList     []string                   `comment:"Optional list of SNI hostnames to use for outbound TLS-capable links.\nIf a peer does not specify ?sni=, a stable SNI is chosen from this list\nper peer (sticky selection) to improve DPI resistance."`
	InterfacePeers      map[string][]string        `comment:"List of connection strings for outbound peer connections in URI format,\narranged by source interface, e.g. { \"eth0\": [ \"tls://a.b.c.d:e\" ] }.\nYou should only use this option if your machine is multi-homed and you\nwant to establish outbound peer connections on different interfaces.\nOtherwise you should use \"Peers\"."`
	Listen              []string                   `comment:"Listen addresses for incoming connections. You will need to add\nlisteners in order to accept incoming peerings from non-local nodes.\nThis is not required if you wish to establish outbound peerings only.\nMulticast peer discovery will work regardless of any listeners set\nhere. Each listener should be specified in URI format as above, e.g.\ntls://0.0.0.0:0 or tls://[::]:0 to listen on all interfaces."`
	AdminListen         string                     `json:",omitempty" comment:"Listen address for admin connections. Default is to listen for local\nconnections either on TCP/9001 or a UNIX socket depending on your\nplatform. Use this value for yggdrasilctl -endpoint=X. To disable\nthe admin socket, use the value \"none\" instead."`
	MulticastInterfaces []MulticastInterfaceConfig `comment:"Configuration for which interfaces multicast peer discovery should be\nenabled on. Regex is a regular expression which is matched against an\ninterface name, and interfaces use the first configuration that they\nmatch against. Beacon controls whether or not your node advertises its\npresence to others, whereas Listen controls whether or not your node\nlistens out for and tries to connect to other advertising nodes. See\nhttps://yggdrasil-network.github.io/configurationref.html#multicastinterfaces\nfor more supported options."`
	AllowedPublicKeys   []string                   `comment:"List of peer public keys to allow incoming peering connections\nfrom. If left empty/undefined then all connections will be allowed\nby default. This does not affect outgoing peerings, nor does it\naffect link-local peers discovered via multicast.\nWARNING: THIS IS NOT A FIREWALL and DOES NOT limit who can reach\nopen ports or services running on your machine!"`
	IfName              string                     `comment:"Local network interface name for TUN adapter, or \"auto\" to select\nan interface automatically, or \"none\" to run without TUN."`
	IfMTU               uint64                     `comment:"Maximum Transmission Unit (MTU) size for your local TUN interface.\nDefault is the largest supported size for your platform. The lowest\npossible value is 1280."`
	LogLookups          bool                       `json:",omitempty"`
	NodeInfoPrivacy     bool                       `comment:"By default, nodeinfo contains some defaults including the platform,\narchitecture and Yggdrasil version. These can help when surveying\nthe network and diagnosing network routing problems. Enabling\nnodeinfo privacy prevents this, so that only items specified in\n\"NodeInfo\" are sent back if specified."`
	NodeInfo            map[string]interface{}     `comment:"Optional nodeinfo. This must be a { \"key\": \"value\", ... } map\nor set as null. This is entirely optional but, if set, is visible\nto the whole network on request."`
	PrometheusEnabled   bool                       `json:",omitempty" comment:"Enable the built-in Prometheus metrics endpoint. Disabled by default."`
	PrometheusListen    string                     `json:",omitempty" comment:"Listen address for the Prometheus metrics HTTP endpoint.\nDefaults to 127.0.0.1:9756 when PrometheusEnabled is true."`
	Community           string                     `json:",omitempty" comment:"Optional community string for private network isolation. When set, only\nnodes with the same community string can peer with this node. The string\nis never transmitted in plaintext — it is used to derive key material\nfor the handshake proof. Leave empty to disable (default)."`
	CommunityMode       string                     `json:",omitempty" comment:"Controls how community-enabled nodes treat peers without community\nsupport. Allowed values are \"strict\" and \"soft\". Strict (default)\nrequires matching community support on both sides. Soft allows outbound\nconnections to peers without the community feature, while still rejecting\ninbound legacy peers and mismatching communities."`
	AddressPrefix       string                     `json:",omitempty" comment:"First byte of the IPv6 address prefix as a two-character hex string\n(e.g. \"02\" = 200::/7, \"04\" = 400::/7, \"fc\" = FC00::/7). Must be an\neven value — the low bit is reserved for internal use. All nodes in\nthe same network must use the same prefix. Defaults to \"02\"."`
	WebUI               WebUIConfig                `comment:"Web interface configuration for managing the node through a browser."`
}

type MulticastInterfaceConfig struct {
	Regex    string
	Beacon   bool
	Listen   bool
	Port     uint16 `json:",omitempty"`
	Priority uint64 `json:",omitempty"` // really uint8, but gobind won't export it
	Password string
}

type WebUIConfig struct {
	Enable   bool   `comment:"Enable the web interface for managing the node through a browser."`
	Port     uint16 `comment:"Port for the web interface. Default is 9000."`
	Host     string `comment:"Host/IP address to bind the web interface to. Empty means all interfaces."`
	Password string `comment:"Password for accessing the web interface. If empty, no authentication is required."`
}

// Generates default configuration and returns a pointer to the resulting
// NodeConfig. This is used when outputting the -genconf parameter and also when
// using -autoconf.
func GenerateConfig() *NodeConfig {
	// Get the defaults for the platform.
	defaults := GetDefaults()
	// Create a node configuration and populate it.
	cfg := new(NodeConfig)
	cfg.NewPrivateKey()
	cfg.Listen = []string{}
	cfg.AdminListen = defaults.DefaultAdminListen
	cfg.Peers = []string{}
	cfg.OutboundSNIList = []string{}
	cfg.InterfacePeers = map[string][]string{}
	cfg.AllowedPublicKeys = []string{}
	cfg.MulticastInterfaces = defaults.DefaultMulticastInterfaces
	cfg.IfName = defaults.DefaultIfName
	cfg.IfMTU = defaults.DefaultIfMTU
	cfg.NodeInfoPrivacy = false
	cfg.PrometheusEnabled = false
	cfg.PrometheusListen = "127.0.0.1:9756"
	cfg.CommunityMode = "strict"
	cfg.WebUI = WebUIConfig{
		Enable:   false,
		Port:     9000,
		Host:     "127.0.0.1",
		Password: "",
	}
	if err := cfg.postprocessConfig(); err != nil {
		panic(err)
	}
	return cfg
}

func (cfg *NodeConfig) ReadFrom(r io.Reader) (int64, error) {
	conf, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	n := int64(len(conf))
	// If there's a byte order mark - which Windows 10 is now incredibly fond of
	// throwing everywhere when it's converting things into UTF-16 for the hell
	// of it - remove it and decode back down into UTF-8. This is necessary
	// because hjson doesn't know what to do with UTF-16 and will panic
	if len(conf) >= 2 && (bytes.Equal(conf[0:2], []byte{0xFF, 0xFE}) ||
		bytes.Equal(conf[0:2], []byte{0xFE, 0xFF})) {
		utf := unicode.UTF16(unicode.BigEndian, unicode.UseBOM)
		decoder := utf.NewDecoder()
		conf, err = decoder.Bytes(conf)
		if err != nil {
			return n, err
		}
	}
	// Generate a new configuration - this gives us a set of sane defaults -
	// then parse the configuration we loaded above on top of it. The effect
	// of this is that any configuration item that is missing from the provided
	// configuration will use a sane default.
	*cfg = *GenerateConfig()
	if err := cfg.UnmarshalHJSON(conf); err != nil {
		return n, err
	}
	return n, nil
}

func (cfg *NodeConfig) UnmarshalHJSON(b []byte) error {
	if err := hjson.Unmarshal(b, cfg); err != nil {
		return err
	}
	return cfg.postprocessConfig()
}

func (cfg *NodeConfig) postprocessConfig() error {
	switch mode := strings.ToLower(strings.TrimSpace(cfg.CommunityMode)); mode {
	case "", "strict":
		cfg.CommunityMode = "strict"
	case "soft":
		cfg.CommunityMode = "soft"
	default:
		return fmt.Errorf("invalid CommunityMode %q: must be \"strict\" or \"soft\"", cfg.CommunityMode)
	}
	if cfg.PrivateKeyPath != "" {
		cfg.PrivateKey = nil
		f, err := os.ReadFile(cfg.PrivateKeyPath)
		if err != nil {
			return err
		}
		if err := cfg.UnmarshalPEMPrivateKey(f); err != nil {
			return err
		}
	}
	switch {
	case cfg.Certificate == nil:
		// No self-signed certificate has been generated yet.
		fallthrough
	case !bytes.Equal(cfg.Certificate.PrivateKey.(ed25519.PrivateKey), cfg.PrivateKey):
		// A self-signed certificate was generated but the private
		// key has changed since then, possibly because a new config
		// was parsed.
		if err := cfg.GenerateSelfSignedCertificate(); err != nil {
			return err
		}
	}
	return nil
}

// RFC5280 section 4.1.2.5
var notAfterNeverExpires = time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)

func (cfg *NodeConfig) GenerateSelfSignedCertificate() error {
	key, err := cfg.MarshalPEMPrivateKey()
	if err != nil {
		return err
	}
	cert, err := cfg.MarshalPEMCertificate()
	if err != nil {
		return err
	}
	tlsCert, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return err
	}
	cfg.Certificate = &tlsCert
	return nil
}

func (cfg *NodeConfig) MarshalPEMCertificate() ([]byte, error) {
	privateKey := ed25519.PrivateKey(cfg.PrivateKey)
	publicKey := privateKey.Public().(ed25519.PublicKey)

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: hex.EncodeToString(publicKey),
		},
		NotBefore:             time.Now(),
		NotAfter:              notAfterNeverExpires,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certbytes, err := x509.CreateCertificate(rand.Reader, cert, cert, publicKey, privateKey)
	if err != nil {
		return nil, err
	}

	block := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certbytes,
	}
	return pem.EncodeToMemory(block), nil
}

func (cfg *NodeConfig) NewPrivateKey() {
	_, spriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic(err)
	}
	cfg.PrivateKey = KeyBytes(spriv)
}

func (cfg *NodeConfig) MarshalPEMPrivateKey() ([]byte, error) {
	b, err := x509.MarshalPKCS8PrivateKey(ed25519.PrivateKey(cfg.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal PKCS8 key: %w", err)
	}
	block := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: b,
	}
	return pem.EncodeToMemory(block), nil
}

func (cfg *NodeConfig) UnmarshalPEMPrivateKey(b []byte) error {
	p, _ := pem.Decode(b)
	if p == nil {
		return fmt.Errorf("failed to parse PEM file")
	}
	if p.Type != "PRIVATE KEY" {
		return fmt.Errorf("unexpected PEM type %q", p.Type)
	}
	k, err := x509.ParsePKCS8PrivateKey(p.Bytes)
	if err != nil {
		return fmt.Errorf("failed to unmarshal PKCS8 key: %w", err)
	}
	key, ok := k.(ed25519.PrivateKey)
	if !ok {
		return fmt.Errorf("private key must be ed25519 key")
	}
	if len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("unexpected ed25519 private key length")
	}
	cfg.PrivateKey = KeyBytes(key)
	return nil
}

type KeyBytes []byte

func (k KeyBytes) MarshalJSON() ([]byte, error) {
	return json.Marshal(hex.EncodeToString(k))
}

func (k *KeyBytes) UnmarshalJSON(b []byte) error {
	var s string
	var err error
	if err = json.Unmarshal(b, &s); err != nil {
		return err
	}
	*k, err = hex.DecodeString(s)
	return err
}

type ConfigInfo struct {
	Path   string      `json:"path"`
	Format string      `json:"format"`
	Data   interface{} `json:"data"`
}

var (
	currentConfigPath string
	currentConfigData *NodeConfig
)

// validateConfigPath validates and cleans a configuration file path to prevent path traversal attacks
func validateConfigPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	// Check for null bytes and other dangerous characters
	if strings.Contains(path, "\x00") {
		return "", fmt.Errorf("path contains null bytes")
	}

	// Check for common path traversal patterns before cleaning
	if strings.Contains(path, "..") || strings.Contains(path, "//") || strings.Contains(path, "\\\\") {
		return "", fmt.Errorf("invalid path: contains path traversal sequences")
	}

	cleanPath := filepath.Clean(path)

	// Convert to absolute path to prevent relative path issues
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %v", err)
	}

	// Double-check for path traversal after cleaning
	if strings.Contains(absPath, "..") {
		return "", fmt.Errorf("path contains traversal sequences after cleaning")
	}

	// Ensure the path is within reasonable bounds (no control characters)
	for _, r := range absPath {
		if r < 32 && r != '\t' && r != '\n' && r != '\r' {
			return "", fmt.Errorf("invalid path: contains control characters")
		}
		if r == 127 || (r >= 128 && r <= 159) {
			return "", fmt.Errorf("invalid path: contains control characters")
		}
	}

	if strings.Count(absPath, "/") > 10 {
		return "", fmt.Errorf("path too deep: potential security risk")
	}

	return absPath, nil
}

func SetCurrentConfig(path string, cfg *NodeConfig) {
	if path != "" {
		if validatedPath, err := validateConfigPath(path); err == nil {
			currentConfigPath = validatedPath
		} else {
			currentConfigPath = "" // swallow the error; webui just won't have a path to offer
		}
	} else {
		currentConfigPath = path
	}
	currentConfigData = cfg
}

func GetCurrentConfig() (*ConfigInfo, error) {
	var configPath string
	var configData *NodeConfig
	format := "hjson"

	if currentConfigPath != "" && currentConfigData != nil {
		validatedCurrentPath, err := validateConfigPath(currentConfigPath)
		if err != nil {
			return nil, fmt.Errorf("invalid current config path: %v", err)
		}
		configPath = validatedCurrentPath
		configData = currentConfigData
	} else {
		defaults := GetDefaults()
		validatedDefaultPath, err := validateConfigPath(defaults.DefaultConfigFile)
		if err != nil {
			return nil, fmt.Errorf("invalid default config path: %v", err)
		}
		configPath = validatedDefaultPath
		configData = GenerateConfig()
	}

	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err == nil {
			cfg := GenerateConfig()
			if err := hjson.Unmarshal(data, cfg); err == nil {
				configData = cfg
				var jsonTest interface{}
				if json.Unmarshal(data, &jsonTest) == nil {
					format = "json"
				}
			} else {
				return nil, fmt.Errorf("failed to parse config file: %v", err)
			}
		}
	}

	return &ConfigInfo{
		Path:   configPath,
		Format: format,
		Data:   configData,
	}, nil
}

func SaveConfig(configData interface{}, configPath, format string) error {
	var testConfig NodeConfig
	configBytes, err := json.Marshal(configData)
	if err != nil {
		return fmt.Errorf("failed to marshal config data: %v", err)
	}

	if err := json.Unmarshal(configBytes, &testConfig); err != nil {
		return fmt.Errorf("invalid configuration data: %v", err)
	}

	targetPath := configPath
	if targetPath == "" {
		if currentConfigPath != "" {
			targetPath = currentConfigPath
		} else {
			defaults := GetDefaults()
			targetPath = defaults.DefaultConfigFile
		}
	}

	// Validate and clean the target path to prevent path traversal attacks
	validatedPath, err := validateConfigPath(targetPath)
	if err != nil {
		return fmt.Errorf("invalid target path: %v", err)
	}
	targetPath = validatedPath

	targetFormat := format
	if targetFormat == "" {
		if _, err := os.Stat(targetPath); err == nil {
			data, readErr := os.ReadFile(targetPath)
			if readErr == nil {
				var jsonTest interface{}
				if json.Unmarshal(data, &jsonTest) == nil {
					targetFormat = "json"
				} else {
					targetFormat = "hjson"
				}
			}
		}
		if targetFormat == "" {
			targetFormat = "hjson"
		}
	}

	dir := filepath.Clean(filepath.Dir(targetPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	var outputData []byte
	if targetFormat == "json" {
		outputData, err = json.MarshalIndent(configData, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %v", err)
		}
	} else {
		outputData, err = hjson.Marshal(configData)
		if err != nil {
			return fmt.Errorf("failed to marshal HJSON: %v", err)
		}
	}

	if err := os.WriteFile(targetPath, outputData, 0o600); err != nil {
		return fmt.Errorf("failed to write config file: %v", err)
	}

	if targetPath == currentConfigPath {
		currentConfigData = &testConfig
	}

	return nil
}
