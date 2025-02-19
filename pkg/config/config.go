package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
	"github.com/gomods/athens/pkg/download/mode"
	"github.com/gomods/athens/pkg/errors"
	"github.com/kelseyhightower/envconfig"
	validator "gopkg.in/go-playground/validator.v9"
)

const defaultConfigFile = "athens.toml"

// Config provides configuration values for all components
type Config struct {
	TimeoutConf
	GoEnv            string    `validate:"required" envconfig:"GO_ENV"`
	GoBinary         string    `validate:"required" envconfig:"GO_BINARY_PATH"`
	GoGetWorkers     int       `validate:"required" envconfig:"ATHENS_GOGET_WORKERS"`
	ProtocolWorkers  int       `validate:"required" envconfig:"ATHENS_PROTOCOL_WORKERS"`
	LogLevel         string    `validate:"required" envconfig:"ATHENS_LOG_LEVEL"`
	CloudRuntime     string    `validate:"required" envconfig:"ATHENS_CLOUD_RUNTIME"`
	FilterFile       string    `envconfig:"ATHENS_FILTER_FILE"`
	TraceExporterURL string    `envconfig:"ATHENS_TRACE_EXPORTER_URL"`
	TraceExporter    string    `envconfig:"ATHENS_TRACE_EXPORTER"`
	StatsExporter    string    `envconfig:"ATHENS_STATS_EXPORTER"`
	StorageType      string    `validate:"required" envconfig:"ATHENS_STORAGE_TYPE"`
	GlobalEndpoint   string    `envconfig:"ATHENS_GLOBAL_ENDPOINT"` // This feature is not yet implemented
	Port             string    `envconfig:"ATHENS_PORT"`
	BasicAuthUser    string    `envconfig:"BASIC_AUTH_USER"`
	BasicAuthPass    string    `envconfig:"BASIC_AUTH_PASS"`
	ForceSSL         bool      `envconfig:"PROXY_FORCE_SSL"`
	ValidatorHook    string    `envconfig:"ATHENS_PROXY_VALIDATOR"`
	PathPrefix       string    `envconfig:"ATHENS_PATH_PREFIX"`
	NETRCPath        string    `envconfig:"ATHENS_NETRC_PATH"`
	GithubToken      string    `envconfig:"ATHENS_GITHUB_TOKEN"`
	HGRCPath         string    `envconfig:"ATHENS_HGRC_PATH"`
	TLSCertFile      string    `envconfig:"ATHENS_TLSCERT_FILE"`
	TLSKeyFile       string    `envconfig:"ATHENS_TLSKEY_FILE"`
	SumDBs           []string  `envconfig:"ATHENS_SUM_DBS"`
	NoSumPatterns    []string  `envconfig:"ATHENS_GONOSUM_PATTERNS"`
	DownloadMode     mode.Mode `envconfig:"ATHENS_DOWNLOAD_MODE"`
	DownloadURL      string    `envconfig:"ATHENS_DOWNLOAD_URL"`
	SingleFlightType string    `envconfig:"ATHENS_SINGLE_FLIGHT_TYPE"`
	SingleFlight     *SingleFlight
	Storage          *StorageConfig
}

// Load loads the config from a file.
// If file is not present returns default config
func Load(configFile string) (*Config, error) {
	// User explicitly specified a config file
	if configFile != "" {
		return ParseConfigFile(configFile)
	}

	// There is a config in the current directory
	if fi, err := os.Stat(defaultConfigFile); err == nil {
		return ParseConfigFile(fi.Name())
	}

	// Use default values
	log.Println("Running dev mode with default settings, consult config when you're ready to run in production")
	cfg := defaultConfig()
	return cfg, envOverride(cfg)
}

func defaultConfig() *Config {
	return &Config{
		GoBinary:         "go",
		GoEnv:            "development",
		GoGetWorkers:     10,
		ProtocolWorkers:  30,
		LogLevel:         "debug",
		CloudRuntime:     "none",
		StatsExporter:    "prometheus",
		TimeoutConf:      TimeoutConf{Timeout: 300},
		StorageType:      "memory",
		Port:             ":3000",
		SingleFlightType: "memory",
		GlobalEndpoint:   "http://localhost:3001",
		TraceExporterURL: "http://localhost:14268",
		SumDBs:           []string{"https://sum.golang.org"},
		NoSumPatterns:    []string{},
		DownloadMode:     "sync",
		DownloadURL:      "",
		SingleFlight: &SingleFlight{
			Etcd:  &Etcd{"localhost:2379,localhost:22379,localhost:32379"},
			Redis: &Redis{"127.0.0.1:6379"},
		},
	}
}

// BasicAuth returns BasicAuthUser and BasicAuthPassword
// and ok if neither of them are empty
func (c *Config) BasicAuth() (user, pass string, ok bool) {
	user = c.BasicAuthUser
	pass = c.BasicAuthPass
	ok = user != "" && pass != ""
	return user, pass, ok
}

// TLSCertFiles returns certificate and key files and an error if
// both files doesn't exist and have approperiate file permissions
func (c *Config) TLSCertFiles() (cert, key string, err error) {
	if c.TLSCertFile == "" && c.TLSKeyFile == "" {
		return "", "", nil
	}

	certFile, err := os.Stat(c.TLSCertFile)
	if err != nil {
		return "", "", fmt.Errorf("Could not access TLSCertFile: %v", err)
	}

	keyFile, err := os.Stat(c.TLSKeyFile)
	if err != nil {
		return "", "", fmt.Errorf("Could not access TLSKeyFile: %v", err)
	}

	if keyFile.Mode()&077 != 0 && runtime.GOOS != "windows" {
		return "", "", fmt.Errorf("TLSKeyFile should not be accessable by others")
	}

	return certFile.Name(), keyFile.Name(), nil
}

// FilterOff returns true if the FilterFile is empty
func (c *Config) FilterOff() bool {
	return c.FilterFile == ""
}

// ParseConfigFile parses the given file into an athens config struct
func ParseConfigFile(configFile string) (*Config, error) {

	var config Config
	// attempt to read the given config file
	if _, err := toml.DecodeFile(configFile, &config); err != nil {
		return nil, err
	}

	// Check file perms from config
	if config.GoEnv == "production" {
		if err := checkFilePerms(configFile, config.FilterFile); err != nil {
			return nil, err
		}
	}

	// override values with environment variables if specified
	if err := envOverride(&config); err != nil {
		return nil, err
	}

	// validate all required fields have been populated
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &config, nil
}

// envOverride uses Environment variables to override unspecified properties
func envOverride(config *Config) error {
	const defaultPort = ":3000"
	err := envconfig.Process("athens", config)
	if err != nil {
		return err
	}
	portEnv := os.Getenv("PORT")
	// ATHENS_PORT takes precedence over PORT
	if portEnv != "" && os.Getenv("ATHENS_PORT") == "" {
		config.Port = portEnv
	}
	if config.Port == "" {
		config.Port = defaultPort
	}
	config.Port = ensurePortFormat(config.Port)
	return nil
}

func ensurePortFormat(s string) string {
	if len(s) == 0 {
		return ""
	}
	if s[0] != ':' {
		return ":" + s
	}
	return s
}

func validateConfig(config Config) error {
	validate := validator.New()
	err := validate.StructExcept(config, "Storage")
	if err != nil {
		return err
	}
	switch config.StorageType {
	case "memory":
		return nil
	case "mongo":
		return validate.Struct(config.Storage.Mongo)
	case "disk":
		return validate.Struct(config.Storage.Disk)
	case "minio":
		return validate.Struct(config.Storage.Minio)
	case "gcp":
		return validate.Struct(config.Storage.GCP)
	case "s3":
		return validate.Struct(config.Storage.S3)
	case "azureblob":
		return validate.Struct(config.Storage.AzureBlob)
	default:
		return fmt.Errorf("storage type %s is unknown", config.StorageType)
	}
}

// GetConf accepts the path to a file, constructs an absolute path to the file,
// and attempts to parse it into a Config struct.
func GetConf(path string) (*Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("Unable to construct absolute path to test config file")
	}
	conf, err := ParseConfigFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("Unable to parse test config file: %s", err.Error())
	}
	return conf, nil
}

// checkFilePerms given a list of files
func checkFilePerms(files ...string) error {
	const op = "config.checkFilePerms"

	for _, f := range files {
		if f == "" {
			continue
		}

		// TODO: Do not ignore errors when a file is not found
		// There is a subtle bug in the filter module which ignores the filter file if it does not find it.
		// This check can be removed once that has been fixed
		fInfo, err := os.Stat(f)
		if err != nil {
			continue
		}

		// Assume unix based system (MacOS and Linux)
		// the bit mask is calculated using the umask command which tells which permissions
		// should not be allowed for a particular user, group or world
		if fInfo.Mode()&0077 != 0 && runtime.GOOS != "windows" {
			return errors.E(op, f+" should have at most rwx,-, - (bit mask 077) as permission")
		}

	}

	return nil
}
