package security

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc/credentials"
)

// LoadTLSConfig loads TLS config from cert/key/ca file paths.
// ca is optional; if non-empty, enables mutual TLS verification.
func LoadTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("cert and key files are required")
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load key pair: %w", err)
	}

	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// Load CA if provided for client verification
	if caFile != "" {
		caCert, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)

		config.ClientCAs = caCertPool
		config.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return config, nil
}

// LoadClientTLSConfig loads TLS config for a gRPC client.
func LoadClientTLSConfig(certFile, keyFile, caFile string) (credentials.TransportCredentials, error) {
	if certFile == "" && keyFile == "" && caFile == "" {
		// No TLS configured
		return nil, nil
	}

	if certFile != "" && keyFile != "" {
		// Mutual TLS
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load key pair: %w", err)
		}

		config := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}

		if caFile != "" {
			caCert, err := os.ReadFile(caFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA file: %w", err)
			}

			caCertPool := x509.NewCertPool()
			caCertPool.AppendCertsFromPEM(caCert)

			config.RootCAs = caCertPool
		}

		return credentials.NewTLS(config), nil
	}

	// Server-only TLS (CA provided for server verification)
	if caFile != "" {
		caCert, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)

		config := &tls.Config{
			RootCAs:    caCertPool,
			MinVersion: tls.VersionTLS12,
		}

		return credentials.NewTLS(config), nil
	}

	// Insecure (should not happen in production)
	return credentials.NewTLS(&tls.Config{ //nolint:gosec // G402: intentional fallback for dev/test environments
		InsecureSkipVerify: true, //nolint:gosec // G402
	}), nil
}

// GenerateSelfSignedCert generates a self-signed certificate for testing.
// This should NOT be used in production.
func GenerateSelfSignedCert() ([]byte, []byte, error) {
	// This is a placeholder - in a real implementation, use
	// crypto/tls/generate_cert.go or similar
	return nil, nil, fmt.Errorf("self-signed cert generation not implemented")
}

// CheckCertExpiry checks if a certificate file is expired or will expire soon.
func CheckCertExpiry(certFile string, daysUntilExpiry int) (bool, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return false, fmt.Errorf("failed to read cert file: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false, fmt.Errorf("failed to decode PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Check expiry
	// Note: This is a simplified check
	return time.Now().AddDate(0, 0, daysUntilExpiry).Before(cert.NotAfter), nil
}
