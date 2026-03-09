package config

// SecurityConfig holds security configuration for all servers.
type SecurityConfig struct {
	JWT   JWTConfig    `mapstructure:"jwt"`
	Guard GuardConfig  `mapstructure:"guard"`
	HTTPS HTTPSConfig  `mapstructure:"https"`
	GRPC  GRPCTLSConfig `mapstructure:"grpc"`
	CORS  CORSConfig   `mapstructure:"cors"`
}

// JWTConfig holds JWT signing configuration.
type JWTConfig struct {
	Signing      JWTKeyConfig `mapstructure:"signing"`
	FilerSigning JWTKeyConfig `mapstructure:"filer_signing"`
}

// JWTKeyConfig holds JWT key configuration.
type JWTKeyConfig struct {
	Key                 string            `mapstructure:"key"`
	ExpiresAfterSeconds int               `mapstructure:"expires_after_seconds"`
	Read                JWTKeyLeafConfig `mapstructure:"read"`
}

// JWTKeyLeafConfig holds read JWT key configuration.
type JWTKeyLeafConfig struct {
	Key                 string `mapstructure:"key"`
	ExpiresAfterSeconds int    `mapstructure:"expires_after_seconds"`
}

// GuardConfig holds access control configuration.
type GuardConfig struct {
	WhiteList string `mapstructure:"white_list"`
}

// TLSCertConfig holds TLS certificate configuration.
type TLSCertConfig struct {
	Cert string `mapstructure:"cert"`
	Key  string `mapstructure:"key"`
	CA   string `mapstructure:"ca"`
}

// HTTPSConfig holds HTTPS TLS configuration for all servers.
type HTTPSConfig struct {
	Master TLSCertConfig `mapstructure:"master"`
	Volume TLSCertConfig `mapstructure:"volume"`
	Filer  TLSCertConfig `mapstructure:"filer"`
	S3     TLSCertConfig `mapstructure:"s3"`
}

// GRPCTLSConfig holds gRPC TLS configuration for all servers.
type GRPCTLSConfig struct {
	Master TLSCertConfig `mapstructure:"master"`
	Volume TLSCertConfig `mapstructure:"volume"`
	Filer  TLSCertConfig `mapstructure:"filer"`
}

// CORSConfig holds CORS configuration.
type CORSConfig struct {
	AllowedOrigins string `mapstructure:"allowed_origins"`
	AllowedMethods string `mapstructure:"allowed_methods"`
	AllowedHeaders string `mapstructure:"allowed_headers"`
}
