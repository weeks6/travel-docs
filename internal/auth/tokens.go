package auth

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
)

const issuer = "travel-docs"

// LoadKeyFiles reads the ES256 key pair used to sign access tokens.
func LoadKeyFiles(privPath, pubPath string) (priv, pub []byte, err error) {
	if priv, err = os.ReadFile(privPath); err != nil {
		return nil, nil, fmt.Errorf("read JWT private key: %w", err)
	}
	if pub, err = os.ReadFile(pubPath); err != nil {
		return nil, nil, fmt.Errorf("read JWT public key: %w", err)
	}
	return priv, pub, nil
}

func parseKeys(privPEM, pubPEM []byte) (*ecdsa.PrivateKey, *ecdsa.PublicKey, error) {
	signKey, err := jwt.ParseECPrivateKeyFromPEM(privPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("parse JWT private key: %w", err)
	}
	verifyKey, err := jwt.ParseECPublicKeyFromPEM(pubPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("parse JWT public key: %w", err)
	}
	return signKey, verifyKey, nil
}

// issueAccessToken returns a signed JWT for userID and its expiry.
func (s *Service) issueAccessToken(userID string) (string, time.Time, error) {
	now := s.now()
	exp := now.Add(s.cfg.AccessTTL)
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   userID,
		ID:        uuid.NewV4().String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	})
	signed, err := token.SignedString(s.signKey)
	return signed, exp, err
}

// parseAccessToken verifies the signature, algorithm, issuer and expiry.
func (s *Service) parseAccessToken(raw string) (*jwt.RegisteredClaims, error) {
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(raw, claims,
		func(*jwt.Token) (any, error) { return s.verifyKey, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil {
		return nil, err
	}
	if claims.Subject == "" || claims.IssuedAt == nil {
		return nil, fmt.Errorf("token is missing sub or iat")
	}
	return claims, nil
}

// newRefreshToken returns a random opaque token and the hash stored in the
// database. Only the hash is persisted, so a database leak doesn't leak
// usable tokens.
func newRefreshToken() (raw string, hash []byte) {
	b := make([]byte, 32)
	rand.Read(b) // never returns an error
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw)
}

func hashToken(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}
