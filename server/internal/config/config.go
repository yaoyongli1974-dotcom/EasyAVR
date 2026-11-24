package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/easyavr/easyavr/internal/security"
)

// Config holds runtime configuration for the EasyAVR server.
// Values are resolved from environment variables with sane defaults so the
// server can boot with zero external dependencies for local development.
type Config struct {
	Env        string // production enables strict startup validation
	Listen     string
	DBDriver   string // sqlite (default) | postgres
	DBPath     string // sqlite file path
	DBDSN      string // postgres DSN, e.g. postgres://user:pass@host:5432/easyavr
	JWTSecret  string
	JWTExpireH int
	AdminUser  string
	AdminPass  string
	// CredentialKey is the AES-256-GCM master key for credentials at rest.
	// It may be base64, hex or a raw passphrase; the key ID is embedded in
	// every ciphertext so keys can be rotated without breaking old rows.
	CredentialKey         string
	CredentialKeyID       string
	CredentialKeyPrevious []string // entries of "<kid>:<key>", used for decryption only
	PGVector              bool     // use pgvector for embeddings when on PostgreSQL
	ZLM                   ZLMConfig
	AllowOrigin           string
	SnapshotDir           string
	MonitorSec            int
	AI                    AIConfig
	GB                    GBConfig
	Cluster               ClusterConfig
	GA1400                GA1400Config
	EHOME                 EHOMEConfig
	GB35114               GB35114Config
	Map                   MapConfig
	Playback              PlaybackConfig
}

// PlaybackConfig governs signed playback URLs (手册 3.7.1.1 播放鉴权/播放时效/播放白名单).
// When Auth is on, play URLs carry an HMAC token that expires after TokenTTLMin.
// Whitelist restricts which Referer/Origin domains may request a play token.
type PlaybackConfig struct {
	Auth        bool
	TokenTTLMin int
	Whitelist   []string
}

// MapConfig configures electronic map helpers such as address search.
// When AMapKey is empty, the platform falls back to OpenStreetMap Nominatim.
type MapConfig struct {
	AMapKey string
}

// EHOMEConfig configures the Hikvision EHOME/ISUP device access endpoint.
type EHOMEConfig struct {
	Enabled   bool
	CMSListen string // UDP CMS port, default :7660
	SMSListen string // UDP SMS port, default :8003
	PublicIP  string
}

// GB35114Config enables the GB35114 (SM crypto) device access mode.
type GB35114Config struct {
	Enabled          bool
	WhiteList        bool
	CertDir          string
	SIPListen        string // GM/T 0024 secure SIP listen, e.g. :5061 (empty disables)
	SIPRequireClient bool   // require a device client certificate (mutual auth)
}

// GA1400Config configures the GA/T1400 (VIID) view library ingest endpoints.
type GA1400Config struct {
	Enabled    bool
	Username   string
	Password   string
	PlatformID string
	NotifyURL  string // this platform's VIID Notification callback base URL
}

// ClusterConfig configures optional multi-node clustering.
type ClusterConfig struct {
	Enabled      bool
	NodeID       string
	Name         string
	APIBase      string // this node's externally reachable API address
	Secret       string // shared secret for inter-node heartbeat
	Peers        []string
	HeartbeatSec int
}

// AIConfig bounds AI task execution so a busy provider cannot exhaust the host.
type AIConfig struct {
	MaxConcurrency  int // global parallel task executions
	PerChannelLimit int // parallel executions per channel
	TaskTimeoutSec  int // hard timeout for one attempt
	MaxRetries      int // retries for transient provider/stream/frame errors
	BackoffBaseMs   int // exponential backoff base in milliseconds
}

// GBConfig configures the GB28181 SIP signaling server.
type GBConfig struct {
	Enabled     bool
	Listen      string // SIP UDP listen, e.g. :5060
	ID          string // platform SIP id (20 digits)
	Realm       string // SIP realm/domain
	Password    string // shared digest password
	RTPIP       string // IP advertised in SDP for RTP receive
	RTPPortLow  int
	RTPPortHigh int
}

// ZLMConfig points at the ZLMediaKit HTTP API and derived media endpoints.
type ZLMConfig struct {
	APIBase     string // e.g. http://127.0.0.1:80
	Secret      string // ZLM api secret (config.ini general.secret)
	MediaHost   string // host reachable by browsers, e.g. 127.0.0.1
	MediaScheme string // http or https; browsers need https when the page is https
	RTSPPort    int
	RTMPPort    int
	HTTPPort    int
	WSPort      int
}

func Load() *Config {
	return &Config{
		Env:                   env("EASYAVR_ENV", "development"),
		Listen:                env("EASYAVR_LISTEN", ":18000"),
		DBDriver:              env("EASYAVR_DB_DRIVER", "sqlite"),
		DBPath:                env("EASYAVR_DB", "./data/easyavr.db"),
		DBDSN:                 env("EASYAVR_DB_DSN", ""),
		JWTSecret:             env("EASYAVR_JWT_SECRET", "easyavr-dev-secret-change-me"),
		JWTExpireH:            envInt("EASYAVR_JWT_EXPIRE_HOURS", 24),
		AdminUser:             env("EASYAVR_ADMIN_USER", "easyavr"),
		AdminPass:             env("EASYAVR_ADMIN_PASS", "easyavr"),
		CredentialKey:         env("EASYAVR_CREDENTIAL_KEY", ""),
		CredentialKeyID:       env("EASYAVR_CREDENTIAL_KEY_ID", "v1"),
		CredentialKeyPrevious: parsePreviousKeys(env("EASYAVR_CREDENTIAL_KEY_PREVIOUS", "")),
		PGVector:              envBool("EASYAVR_PGVECTOR", true),
		AllowOrigin:           env("EASYAVR_ALLOW_ORIGIN", "*"),
		SnapshotDir:           env("EASYAVR_SNAPSHOT_DIR", "./data/snapshots"),
		MonitorSec:            envInt("EASYAVR_MONITOR_SEC", 0),
		AI: AIConfig{
			MaxConcurrency:  envInt("EASYAVR_AI_MAX_CONCURRENCY", 4),
			PerChannelLimit: envInt("EASYAVR_AI_PER_CHANNEL_LIMIT", 1),
			TaskTimeoutSec:  envInt("EASYAVR_AI_TASK_TIMEOUT_SEC", 120),
			MaxRetries:      envInt("EASYAVR_AI_MAX_RETRIES", 2),
			BackoffBaseMs:   envInt("EASYAVR_AI_BACKOFF_BASE_MS", 2000),
		},
		GB: GBConfig{
			Enabled:     envBool("EASYAVR_GB_ENABLED", false),
			Listen:      env("EASYAVR_GB_LISTEN", ":5060"),
			ID:          env("EASYAVR_GB_ID", "34020000002000000001"),
			Realm:       env("EASYAVR_GB_REALM", "3402000000"),
			Password:    env("EASYAVR_GB_PASSWORD", "easyavr123"),
			RTPIP:       env("EASYAVR_GB_RTP_IP", "127.0.0.1"),
			RTPPortLow:  envInt("EASYAVR_GB_RTP_PORT_LOW", 30000),
			RTPPortHigh: envInt("EASYAVR_GB_RTP_PORT_HIGH", 30500),
		},
		Cluster: ClusterConfig{
			Enabled:      envBool("EASYAVR_CLUSTER_ENABLED", false),
			NodeID:       env("EASYAVR_CLUSTER_NODE_ID", "node-1"),
			Name:         env("EASYAVR_CLUSTER_NAME", "easyavr-1"),
			APIBase:      env("EASYAVR_CLUSTER_API", ""),
			Secret:       env("EASYAVR_CLUSTER_SECRET", "easyavr-cluster-secret"),
			Peers:        envList("EASYAVR_CLUSTER_PEERS"),
			HeartbeatSec: envInt("EASYAVR_CLUSTER_HEARTBEAT_SEC", 15),
		},
		GA1400: GA1400Config{
			Enabled:    envBool("EASYAVR_GA1400_ENABLED", false),
			Username:   env("EASYAVR_GA1400_USER", "easyavr"),
			Password:   env("EASYAVR_GA1400_PASSWORD", "easyavr123"),
			PlatformID: env("EASYAVR_GA1400_PLATFORM_ID", "34020000002000000001"),
			NotifyURL:  env("EASYAVR_GA1400_NOTIFY_URL", ""),
		},
		EHOME: EHOMEConfig{
			Enabled:   envBool("EASYAVR_EHOME_ENABLED", false),
			CMSListen: env("EASYAVR_EHOME_CMS_LISTEN", ":7660"),
			SMSListen: env("EASYAVR_EHOME_SMS_LISTEN", ":8003"),
			PublicIP:  env("EASYAVR_EHOME_PUBLIC_IP", "127.0.0.1"),
		},
		GB35114: GB35114Config{
			Enabled:          envBool("EASYAVR_GB35114_ENABLED", false),
			WhiteList:        envBool("EASYAVR_GB35114_WHITELIST", false),
			CertDir:          env("EASYAVR_GB35114_CERT_DIR", "./data/gb35114-certs"),
			SIPListen:        env("EASYAVR_GB35114_SIP_LISTEN", ""),
			SIPRequireClient: envBool("EASYAVR_GB35114_SIP_REQUIRE_CLIENT", true),
		},
		Map: MapConfig{
			AMapKey: env("EASYAVR_AMAP_KEY", ""),
		},
		Playback: PlaybackConfig{
			Auth:        envBool("EASYAVR_PLAY_AUTH", false),
			TokenTTLMin: envInt("EASYAVR_PLAY_TOKEN_TTL_MIN", 60),
			Whitelist:   envList("EASYAVR_PLAY_WHITELIST"),
		},
		ZLM: ZLMConfig{
			APIBase:     strings.TrimRight(env("EASYAVR_ZLM_API", "http://127.0.0.1:80"), "/"),
			Secret:      env("EASYAVR_ZLM_SECRET", "EasyAVRzlmSecret2024x"),
			MediaHost:   env("EASYAVR_MEDIA_HOST", "127.0.0.1"),
			MediaScheme: env("EASYAVR_MEDIA_SCHEME", "http"),
			RTSPPort:    envInt("EASYAVR_ZLM_RTSP_PORT", 554),
			RTMPPort:    envInt("EASYAVR_ZLM_RTMP_PORT", 1935),
			HTTPPort:    envInt("EASYAVR_ZLM_HTTP_PORT", 80),
			WSPort:      envInt("EASYAVR_ZLM_WS_PORT", 80),
		},
	}
}

// IsProduction reports whether strict startup checks apply.
func (c *Config) IsProduction() bool {
	switch strings.ToLower(strings.TrimSpace(c.Env)) {
	case "production", "prod":
		return true
	}
	return false
}

// Validate rejects unsafe configuration before the service starts in
// production. Error messages name the setting but never include its value.
func (c *Config) Validate() error {
	if !c.IsProduction() {
		return nil
	}
	var problems []string
	if c.JWTSecret == "easyavr-dev-secret-change-me" || len(c.JWTSecret) < 16 {
		problems = append(problems, "EASYAVR_JWT_SECRET is a default or too short")
	}
	if c.AdminPass == "easyavr" || len(c.AdminPass) < 8 {
		problems = append(problems, "EASYAVR_ADMIN_PASS is a default or too short")
	}
	if _, err := security.ParseKey(c.CredentialKey); err != nil {
		problems = append(problems, "EASYAVR_CREDENTIAL_KEY is missing or invalid")
	}
	if strings.Contains(c.AllowOrigin, "*") {
		problems = append(problems, "EASYAVR_ALLOW_ORIGIN must not contain a wildcard in production")
	}
	if c.Cluster.Enabled {
		if c.Cluster.Secret == "easyavr-cluster-secret" || len(c.Cluster.Secret) < 16 {
			problems = append(problems, "EASYAVR_CLUSTER_SECRET is a default or too short")
		}
	}
	if c.GB.Enabled {
		if len(c.GB.ID) != 20 || c.GB.Realm == "" || c.GB.Password == "" || c.GB.Password == "easyavr123" {
			problems = append(problems, "EASYAVR_GB_ID / REALM / PASSWORD are incomplete or use defaults")
		}
	}
	if c.GB35114.Enabled {
		if strings.TrimSpace(c.GB35114.CertDir) == "" {
			problems = append(problems, "EASYAVR_GB35114_CERT_DIR is empty")
		} else if err := checkWritableDir(c.GB35114.CertDir); err != nil {
			problems = append(problems, "EASYAVR_GB35114_CERT_DIR is not a writable directory")
		}
	}
	switch strings.ToLower(strings.TrimSpace(c.DBDriver)) {
	case "", "sqlite", "sqlite3":
		if strings.TrimSpace(c.DBPath) == "" {
			problems = append(problems, "EASYAVR_DB is empty")
		} else if err := checkWritableDir(filepath.Dir(c.DBPath)); err != nil {
			problems = append(problems, "EASYAVR_DB parent directory is not writable")
		}
	case "postgres", "postgresql", "pgx":
		if strings.TrimSpace(c.DBDSN) == "" {
			problems = append(problems, "EASYAVR_DB_DSN is empty")
		}
	default:
		problems = append(problems, "EASYAVR_DB_DRIVER is unsupported")
	}
	if c.AI.MaxConcurrency < 1 {
		problems = append(problems, "EASYAVR_AI_MAX_CONCURRENCY must be at least 1")
	}
	if c.AI.PerChannelLimit < 1 {
		problems = append(problems, "EASYAVR_AI_PER_CHANNEL_LIMIT must be at least 1")
	}
	if c.AI.TaskTimeoutSec < 1 {
		problems = append(problems, "EASYAVR_AI_TASK_TIMEOUT_SEC must be at least 1")
	}
	if c.AI.MaxRetries < 0 {
		problems = append(problems, "EASYAVR_AI_MAX_RETRIES must not be negative")
	}
	if c.AI.BackoffBaseMs < 0 {
		problems = append(problems, "EASYAVR_AI_BACKOFF_BASE_MS must not be negative")
	}
	if err := checkWritableDir(c.SnapshotDir); err != nil {
		problems = append(problems, "EASYAVR_SNAPSHOT_DIR is not a writable directory")
	}
	if len(problems) > 0 {
		return fmt.Errorf("production configuration invalid: %s", strings.Join(problems, "; "))
	}
	return nil
}

func checkWritableDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("empty path")
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(path, ".easyavr-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func parsePreviousKeys(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// DatabaseDSN returns the effective DSN for the configured driver: the file
// path for sqlite, or DBDSN for postgres.
func (c *Config) DatabaseDSN() string {
	if c.DBDSN != "" {
		return c.DBDSN
	}
	return c.DBPath
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envList(k string) []string {
	v := os.Getenv(k)
	if v == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}
