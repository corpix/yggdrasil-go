package core

// This file contains the version metadata struct
// Used in the initial connection setup and key exchange
// Some of this could arguably go in wire.go instead

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/blake2b"
)

// This is the version-specific metadata exchanged at the start of a connection.
// It must always begin with the 4 bytes "meta" and a wire formatted uint64 major version number.
// The current version also includes a minor version number, and the box/sig/link keys that need to be exchanged to open a connection.
type version_metadata struct {
	majorVer       uint16
	minorVer       uint16
	publicKey      ed25519.PublicKey
	priority       uint8
	features       uint64
	communityProof []byte // 64-byte blake2b-512 HMAC; nil when FeatureCommunity is not set
	nodeInfo       []byte // NodeInfo data from configuration
}

const (
	ProtocolVersionMajor uint16 = 0
	ProtocolVersionMinor uint16 = 5
)

// Feature flags carried in the metaFeatures TLV field.
const (
	// FeatureCommunity indicates the node requires community string matching.
	// Nodes without this flag or with a different community are rejected.
	FeatureCommunity uint64 = 1 << 0
)

// Once a major/minor version is released, it is not safe to change any of these
// (including their ordering), it is only safe to add new ones.
const (
	metaVersionMajor   uint16 = iota // uint16
	metaVersionMinor                 // uint16
	metaPublicKey                    // [32]byte
	metaPriority                     // uint8
	metaFeatures                     // uint64 — only emitted when non-zero; old nodes skip it
	metaCommunityProof               // [64]byte blake2b-512(key=community)[pubkey]; old nodes skip it
	metaNodeInfo                     // []byte
)

type handshakeError string

func (e handshakeError) Error() string { return string(e) }

const (
	ErrHandshakeInvalidPreamble   = handshakeError("invalid handshake, remote side is not Yggdrasil")
	ErrHandshakeInvalidLength     = handshakeError("invalid handshake length, possible version mismatch")
	ErrHandshakeInvalidPassword   = handshakeError("invalid password supplied, check your config")
	ErrHandshakeHashFailure       = handshakeError("invalid hash length")
	ErrHandshakeIncorrectPassword = handshakeError("password does not match remote side")
	ErrHandshakeCommunityRequired = handshakeError("remote node does not support the community feature")
	ErrHandshakeCommunityMismatch = handshakeError("community string does not match remote node")
)

// Gets a base metadata with no keys set, but with the correct version numbers.
func version_getBaseMetadata() version_metadata {
	return version_metadata{
		majorVer: ProtocolVersionMajor,
		minorVer: ProtocolVersionMinor,
	}
}

// communityProofKey returns the BLAKE2b keyed-hash key for a configured
// community string. Short values are used directly for compatibility, while
// longer values are reduced to a fixed-size digest first.
func communityProofKey(community []byte) []byte {
	if len(community) <= blake2b.Size {
		return community
	}
	key := blake2b.Sum512(community)
	return key[:]
}

// newCommunityProof computes the community handshake proof for a public key.
// The proof is blake2b-512 keyed with key material derived from the community
// string over the public key. It proves knowledge of the community string
// without revealing it.
func newCommunityProof(community []byte, pubkey ed25519.PublicKey) ([]byte, error) {
	hasher, err := blake2b.New512(communityProofKey(community))
	if err != nil {
		return nil, err
	}
	hasher.Write(pubkey)
	return hasher.Sum(nil), nil
}

// verifyCommunityProof checks that proof was produced by a node with the given
// community string for the given public key.
func verifyCommunityProof(community, proof []byte, pubkey ed25519.PublicKey) bool {
	expected, err := newCommunityProof(community, pubkey)
	if err != nil {
		return false
	}
	return bytes.Equal(expected, proof)
}

// Encodes version metadata into its wire format.
func (m *version_metadata) encode(privateKey ed25519.PrivateKey, password []byte) ([]byte, error) {
	bs := make([]byte, 0, 64)
	bs = append(bs, 'm', 'e', 't', 'a')
	bs = append(bs, 0, 0) // Remaining message length placeholder

	bs = binary.BigEndian.AppendUint16(bs, metaVersionMajor)
	bs = binary.BigEndian.AppendUint16(bs, 2)
	bs = binary.BigEndian.AppendUint16(bs, m.majorVer)

	bs = binary.BigEndian.AppendUint16(bs, metaVersionMinor)
	bs = binary.BigEndian.AppendUint16(bs, 2)
	bs = binary.BigEndian.AppendUint16(bs, m.minorVer)

	bs = binary.BigEndian.AppendUint16(bs, metaPublicKey)
	bs = binary.BigEndian.AppendUint16(bs, ed25519.PublicKeySize)
	bs = append(bs, m.publicKey[:]...)

	bs = binary.BigEndian.AppendUint16(bs, metaPriority)
	bs = binary.BigEndian.AppendUint16(bs, 1)
	bs = append(bs, m.priority)

	if m.features != 0 {
		bs = binary.BigEndian.AppendUint16(bs, metaFeatures)
		bs = binary.BigEndian.AppendUint16(bs, 8)
		bs = binary.BigEndian.AppendUint64(bs, m.features)
	}

	if len(m.communityProof) > 0 {
		bs = binary.BigEndian.AppendUint16(bs, metaCommunityProof)
		bs = binary.BigEndian.AppendUint16(bs, uint16(len(m.communityProof)))
		bs = append(bs, m.communityProof...)
	}

	if len(m.nodeInfo) > 0 {
		if len(m.nodeInfo) > 16384 {
			return nil, fmt.Errorf("NodeInfo exceeds max length of 16384 bytes")
		}
		bs = binary.BigEndian.AppendUint16(bs, metaNodeInfo)
		bs = binary.BigEndian.AppendUint16(bs, uint16(len(m.nodeInfo)))
		bs = append(bs, m.nodeInfo...)
	}

	hasher, err := blake2b.New512(password)
	if err != nil {
		return nil, ErrHandshakeInvalidPassword
	}
	if n, err := hasher.Write(m.publicKey); err != nil || n != ed25519.PublicKeySize {
		return nil, ErrHandshakeHashFailure
	}
	hash := hasher.Sum(nil)
	bs = append(bs, ed25519.Sign(privateKey, hash)...)

	binary.BigEndian.PutUint16(bs[4:6], uint16(len(bs)-6))
	return bs, nil
}

// decodeTLV reads and parses the TLV fields from the wire without verifying
// the trailing signature. Returns the raw signature bytes for the caller to
// verify after inspecting the decoded fields (e.g. feature flags).
func (m *version_metadata) decodeTLV(r io.Reader) (sig []byte, err error) {
	bh := [6]byte{}
	if _, err := io.ReadFull(r, bh[:]); err != nil {
		return nil, err
	}
	if !bytes.Equal(bh[:4], []byte("meta")) {
		return nil, ErrHandshakeInvalidPreamble
	}
	hl := binary.BigEndian.Uint16(bh[4:6])
	if hl < ed25519.SignatureSize {
		return nil, ErrHandshakeInvalidLength
	}
	bs := make([]byte, hl)
	if _, err := io.ReadFull(r, bs); err != nil {
		return nil, err
	}
	sig = append([]byte(nil), bs[len(bs)-ed25519.SignatureSize:]...)
	bs = bs[:len(bs)-ed25519.SignatureSize]

	for len(bs) >= 4 {
		op := binary.BigEndian.Uint16(bs[:2])
		oplen := int(binary.BigEndian.Uint16(bs[2:4]))
		if bs = bs[4:]; len(bs) < oplen {
			return nil, ErrHandshakeInvalidLength
		}
		field := bs[:oplen]
		switch op {
		case metaVersionMajor:
			if len(field) != 2 {
				return nil, ErrHandshakeInvalidLength
			}
			m.majorVer = binary.BigEndian.Uint16(field)

		case metaVersionMinor:
			if len(field) != 2 {
				return nil, ErrHandshakeInvalidLength
			}
			m.minorVer = binary.BigEndian.Uint16(field)

		case metaPublicKey:
			if len(field) != ed25519.PublicKeySize {
				return nil, ErrHandshakeInvalidLength
			}
			m.publicKey = append(m.publicKey[:0], field...)

		case metaPriority:
			if len(field) != 1 {
				return nil, ErrHandshakeInvalidLength
			}
			m.priority = field[0]
		case metaFeatures:
			if oplen >= 8 {
				m.features = binary.BigEndian.Uint64(field[:8])
			}
		case metaCommunityProof:
			m.communityProof = append([]byte(nil), field...)
		case metaNodeInfo:
			if oplen > 16384 {
				return nil, fmt.Errorf("received NodeInfo exceeds max length of 16384 bytes")
			}
			m.nodeInfo = make([]byte, oplen)
			copy(m.nodeInfo, field)
		}
		bs = bs[oplen:]
	}
	if len(bs) != 0 {
		return nil, ErrHandshakeInvalidLength
	}
	return sig, nil
}

// verifySignature authenticates the handshake using the provided password.
// Must be called after decodeTLV so that m.publicKey is populated.
func (m *version_metadata) verifySignature(sig, password []byte) error {
	hasher, err := blake2b.New512(password)
	if err != nil {
		return ErrHandshakeInvalidPassword
	}
	if n, err := hasher.Write(m.publicKey); err != nil || n != ed25519.PublicKeySize {
		return ErrHandshakeHashFailure
	}
	hash := hasher.Sum(nil)
	if !ed25519.Verify(m.publicKey, hash, sig) {
		return ErrHandshakeIncorrectPassword
	}
	return nil
}

// decode is the combined convenience method for callers that do not need to
// inspect feature flags before signature verification (e.g. multicast).
func (m *version_metadata) decode(r io.Reader, password []byte) error {
	sig, err := m.decodeTLV(r)
	if err != nil {
		return err
	}
	return m.verifySignature(sig, password)
}

// check validates that the version numbers and public key are well-formed and
// match the expected protocol version.
func (m *version_metadata) check() bool {
	switch {
	case m.majorVer != ProtocolVersionMajor:
		return false
	case m.minorVer != ProtocolVersionMinor:
		return false
	case len(m.publicKey) != ed25519.PublicKeySize:
		return false
	default:
		return true
	}
}
