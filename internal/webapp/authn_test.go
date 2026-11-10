package webapp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/kumoSleeping/iridium/internal/gate"
)

// authenticator is a minimal software passkey provider: ES256, "none" attestation,
// user verification, backup-eligible (like a synced passkey).
type authenticator struct {
	t      *testing.T
	origin string
	creds  []*vcred
	// count, if set, makes the signature counter advance by one per use (a security key);
	// otherwise it stays 0 (a synced passkey).
	count bool
}

type vcred struct {
	id     []byte
	key    *ecdsa.PrivateKey
	rpID   string
	user   []byte
	signed uint32
}

var b64 = base64.RawURLEncoding

const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
)

type creationOptions struct {
	Challenge string `json:"challenge"`
	RP        struct {
		ID string `json:"id"`
	} `json:"rp"`
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	PubKeyCredParams []struct {
		Alg int `json:"alg"`
	} `json:"pubKeyCredParams"`
}

type requestOptions struct {
	Challenge string `json:"challenge"`
	RPID      string `json:"rpId"`
}

func (a *authenticator) clientData(typ, challenge string) []byte {
	cd, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	return cd
}

// create answers navigator.credentials.create with the options the server sent.
func (a *authenticator) create(options json.RawMessage) json.RawMessage {
	a.t.Helper()
	var o creationOptions
	if err := json.Unmarshal(options, &o); err != nil {
		a.t.Fatalf("creation options: %v", err)
	}
	es256 := false
	for _, p := range o.PubKeyCredParams {
		es256 = es256 || p.Alg == -7
	}
	if !es256 {
		a.t.Fatalf("server does not offer ES256")
	}
	user, err := b64.DecodeString(o.User.ID)
	if err != nil {
		a.t.Fatalf("user id: %v", err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c := &vcred{id: gate.RandomBytes(16), key: key, rpID: o.RP.ID, user: user}
	a.creds = append(a.creds, c)

	pub, _ := key.PublicKey.ECDH()
	pt := pub.Bytes() // 0x04 || X || Y
	cose, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: 2, Algorithm: -7},
		Curve:         1,
		XCoord:        pt[1:33],
		YCoord:        pt[33:65],
	})
	if err != nil {
		a.t.Fatal(err)
	}
	rpHash := sha256.Sum256([]byte(c.rpID))
	auth := append([]byte{}, rpHash[:]...)
	auth = append(auth, flagUP|flagUV|flagBE|flagBS|flagAT)
	auth = binary.BigEndian.AppendUint32(auth, 0)
	auth = append(auth, make([]byte, 16)...) // AAGUID
	auth = binary.BigEndian.AppendUint16(auth, uint16(len(c.id)))
	auth = append(auth, c.id...)
	auth = append(auth, cose...)
	att, err := webauthncbor.Marshal(struct {
		Fmt      string         `cbor:"fmt"`
		AttStmt  map[string]any `cbor:"attStmt"`
		AuthData []byte         `cbor:"authData"`
	}{"none", map[string]any{}, auth})
	if err != nil {
		a.t.Fatal(err)
	}
	out, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(c.id), "rawId": b64.EncodeToString(c.id), "type": "public-key",
		"authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(a.clientData("webauthn.create", o.Challenge)),
			"attestationObject": b64.EncodeToString(att),
			"transports":        []string{"internal", "hybrid"},
		},
	})
	return out
}

// get answers navigator.credentials.get with credential c (default: the newest).
func (a *authenticator) get(options json.RawMessage, c *vcred) json.RawMessage {
	a.t.Helper()
	var o requestOptions
	if err := json.Unmarshal(options, &o); err != nil {
		a.t.Fatalf("request options: %v", err)
	}
	if c == nil {
		c = a.creds[len(a.creds)-1]
	}
	if a.count {
		c.signed++
	}
	cd := a.clientData("webauthn.get", o.Challenge)
	rpHash := sha256.Sum256([]byte(o.RPID))
	auth := append([]byte{}, rpHash[:]...)
	auth = append(auth, flagUP|flagUV|flagBE|flagBS)
	auth = binary.BigEndian.AppendUint32(auth, c.signed)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, auth...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, c.key, digest[:])
	if err != nil {
		a.t.Fatal(err)
	}
	out, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(c.id), "rawId": b64.EncodeToString(c.id), "type": "public-key",
		"authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(auth),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(c.user),
		},
	})
	return out
}
