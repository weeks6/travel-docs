package auth

import (
	"crypto/ecdsa"
	"os"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
	"github.com/weeks6/travel-docs/util"
)

const (
	privKeyPath = "test_key.pem"
	pubKeyPath  = "test_key.pub.pem"
)

var (
	verifyKey *ecdsa.PublicKey
	signKey   *ecdsa.PrivateKey
)

type UserPayload struct {
	UserId string `json:"user_id"`
}

type TokenClaims struct {
	jwt.RegisteredClaims
	UserPayload
}

func InitJWT() {
	signBytes, err := os.ReadFile(privKeyPath)
	util.LogFatal(err)

	signKey, err = jwt.ParseECPrivateKeyFromPEM(signBytes)
	util.LogFatal(err)

	verifyBytes, err := os.ReadFile(pubKeyPath)
	util.LogFatal(err)

	verifyKey, err = jwt.ParseECPublicKeyFromPEM(verifyBytes)
	util.LogFatal(err)
}

func createJWTToken(payload UserPayload) (string, error) {
	token := jwt.New(jwt.GetSigningMethod("ES256"))

	token.Claims = &TokenClaims{
		jwt.RegisteredClaims{
			ID:        uuid.NewV4().String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute * 1)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		payload,
	}

	return token.SignedString(signKey)
}
