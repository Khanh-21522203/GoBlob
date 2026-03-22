package security

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// SignJWT creates a signed JWT with the given signing key and expiry.
// Uses HS256 (HMAC-SHA256) algorithm.
// Returns signed token string or error.
func SignJWT(key string, expiresAfterSec int) (string, error) {
	if key == "" {
		return "", fmt.Errorf("signing key cannot be empty")
	}

	claims := jwt.MapClaims{
		"exp": time.Now().Add(time.Duration(expiresAfterSec) * time.Second).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(key))
}

// VerifyJWT verifies a JWT token string against the given key.
// Returns claims map or error if invalid/expired.
func VerifyJWT(tokenString, key string) (jwt.MapClaims, error) {
	if key == "" {
		return nil, fmt.Errorf("verification key cannot be empty")
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method - only allow HS256
		if method, ok := token.Method.(*jwt.SigningMethodHMAC); !ok || method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(key), nil
	}, jwt.WithValidMethods([]string{"HS256"}))

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

// GenerateSigningKey generates a cryptographically random signing key.
// length is the number of random bytes before base64 encoding; the returned
// string will be ~4/3 * length characters. Minimum 32 bytes.
func GenerateSigningKey(length int) (string, error) {
	if length <= 0 {
		length = 32
	}
	key := make([]byte, length)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate signing key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}
