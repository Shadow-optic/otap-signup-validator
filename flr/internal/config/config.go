package config

import "time"

// Config holds the FLR node configuration
type Config struct {
	Node        NodeConfig        `mapstructure:"node"`
	Registry    RegistryConfig    `mapstructure:"registry"`
	Crypto      CryptoConfig      `mapstructure:"crypto"`
	Federation  FederationConfig  `mapstructure:"federation"`
	Operators   []OperatorConfig  `mapstructure:"operators"`
	Server      ServerConfig      `mapstructure:"server"`
	Logging     LoggingConfig     `mapstructure:"logging"`
	Compliance  ComplianceConfig  `mapstructure:"compliance"`
}

type NodeConfig struct {
	ID         string `mapstructure:"id"`
	Name       string `mapstructure:"name"`
	ListenAddr string `mapstructure:"listen_addr"`
}

type RegistryConfig struct {
	DBPath         string        `mapstructure:"db_path"`
	MerkleInterval time.Duration `mapstructure:"merkle_interval"`
}

type CryptoConfig struct {
	PrivateKeyPath string `mapstructure:"private_key_path"`
	PublicKeyPath  string `mapstructure:"public_key_path"`
}

type FederationConfig struct {
	GossipInterval time.Duration `mapstructure:"gossip_interval"`
	SyncTimeout    time.Duration `mapstructure:"sync_timeout"`
	MaxPeers       int           `mapstructure:"max_peers"`
}

type OperatorConfig struct {
	ID            string `mapstructure:"id"`
	Name          string `mapstructure:"name"`
	Endpoint      string `mapstructure:"endpoint"`
	PublicKeyPath string `mapstructure:"public_key_path"`
}

type ServerConfig struct {
	GRPCAddr     string        `mapstructure:"grpc_addr"`
	HTTPAddr     string        `mapstructure:"http_addr"`
	TLSCert      string        `mapstructure:"tls_cert"`
	TLSKey       string        `mapstructure:"tls_key"`
	ClientCA     string        `mapstructure:"client_ca"`
	EnableAuth   bool          `mapstructure:"enable_auth"`
	MaxConn      int           `mapstructure:"max_conn"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

type ComplianceConfig struct {
	ITUGrid      string  `mapstructure:"itu_grid"`
	GridSpacingGHz float64 `mapstructure:"grid_spacing_ghz"`
}

// Defaults returns default configuration
func Defaults() *Config {
	return &Config{
		Node: NodeConfig{
			ID:         "op-001",
			Name:       "OTAP Network Operator",
			ListenAddr: "0.0.0.0:9090",
		},
		Registry: RegistryConfig{
			DBPath:         "./data/registry",
			MerkleInterval: 5 * time.Minute,
		},
		Federation: FederationConfig{
			GossipInterval: 30 * time.Second,
			SyncTimeout:    10 * time.Second,
			MaxPeers:       50,
		},
		Server: ServerConfig{
			GRPCAddr:     ":9090",
			HTTPAddr:     ":8080",
			EnableAuth:   true,
			MaxConn:      1000,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Compliance: ComplianceConfig{
			ITUGrid:        "C_BAND",
			GridSpacingGHz: 25.0,
		},
	}
}
