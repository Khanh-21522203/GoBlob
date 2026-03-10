package wdclient

import (
	"GoBlob/goblob/config"
	"GoBlob/goblob/security"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GetDialOption returns a grpc.DialOption based on the provided security config.
// If secCfg is nil or has no cert file configured, insecure credentials are used.
func GetDialOption(secCfg *config.SecurityConfig) (grpc.DialOption, error) {
	if secCfg == nil || secCfg.GRPC.Master.Cert == "" {
		return grpc.WithTransportCredentials(insecure.NewCredentials()), nil
	}

	cfg := secCfg.GRPC.Master
	creds, err := security.LoadClientTLSConfig(cfg.Cert, cfg.Key, cfg.CA)
	if err != nil {
		return nil, err
	}
	return grpc.WithTransportCredentials(creds), nil
}
