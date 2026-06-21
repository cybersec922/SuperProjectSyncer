package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
)

type ecdsaPrivateKey = ecdsa.PrivateKey

func newECDSAKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}
