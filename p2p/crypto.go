package p2p

import (
	"crypto/ecdsa"

	"github.com/ethereum/go-ethereum/crypto"
	pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/pkg/errors"
)

func LoadPrivateKey(dataDir string) (*ecdsa.PrivateKey, error) {
	// TODO(galaio): try load key from datadir first,
	// create a new key when there is no key found
	// save the new key in the last
	priv, err := crypto.GenerateKey()
	if err != nil {
		return nil, errors.Wrapf(err, "GenerateKey err")
	}
	return priv, nil
}

func ConvertToInterfacePrivkey(privkey *ecdsa.PrivateKey) (pcrypto.PrivKey, error) {
	privBytes := privkey.D.Bytes()
	// In the event the number of bytes outputted by the big-int are less than 32,
	// we append bytes to the start of the sequence for the missing most significant
	// bytes.
	if len(privBytes) < 32 {
		privBytes = append(make([]byte, 32-len(privBytes)), privBytes...)
	}
	return pcrypto.UnmarshalSecp256k1PrivateKey(privBytes)
}
