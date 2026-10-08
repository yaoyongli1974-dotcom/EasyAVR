package config

import (
	"os"
	"strconv"
	"strings"
)

// Config holds runtime configuration for the EasyAVR server.
// Values are resolved from environment variables with sane defaults so the
// server can boot with zero external dependencies for local development.
type Config struct {
	Listen      string
	DBDriver    string // sqlite (default) | postgres
	DBPath      string // sqlite file path
	DBDSN       string // postgres DSN, e.g. postgres://user:pass@host:5432/easyavr
	JWTSecret   string
	JWTExpireH  int
	AdminUser   string
	AdminPass   string
	PGVector    bool // use pgvector for embeddings when on PostgreSQL
	ZLM         ZLMConfig
	AllowOrigin string
	SnapshotDir string
	GB          GBConfig
	Cluster     ClusterConfig
	GA1400      GA1400Config
	EHOME       EHOMEConfig
	GB35114     GB35114Config
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
	APIBase   string // e.g. http://127.0.0.1:80
	Secret    string // ZLM api secret (config.ini general.secret)
	MediaHost string // host reachable by browsers, e.g. 127.0.0.1
	RTSPPort  int
	RTMPPort  int
	HTTPPort  int
	WSPort    int
}

func Load() *Config {
	return &Config{
		Listen:      env("EASYAVR_LISTEN", ":18000"),
		DBDriver:    env("EASYAVR_DB_DRIVER", "sqlite"),
		DBPath:      env("EASYAVR_DB", "./data/easyavr.db"),
		DBDSN:       env("EASYAVR_DB_DSN", ""),
		JWTSecret:   env("EASYAVR_JWT_SECRET", "easyavr-dev-secret-change-me"),
		JWTExpireH:  envInt("EASYAVR_JWT_EXPIRE_HOURS", 24),
		AdminUser:   env("EASYAVR_ADMIN_USER", "easyavr"),
		AdminPass:   env("EASYAVR_ADMIN_PASS", "easyavr"),
		PGVector:    envBool("EASYAVR_PGVECTOR", true),
		AllowOrigin: env("EASYAVR_ALLOW_ORIGIN", "*"),
		SnapshotDir: env("EASYAVR_SNAPSHOT_DIR", "./data/snapshots"),
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
		ZLM: ZLMConfig{
			APIBase:   strings.TrimRight(env("EASYAVR_ZLM_API", "http://127.0.0.1:80"), "/"),
			Secret:    env("EASYAVR_ZLM_SECRET", "EasyAVRzlmSecret2024x"),
			MediaHost: env("EASYAVR_MEDIA_HOST", "127.0.0.1"),
			RTSPPort:  envInt("EASYAVR_ZLM_RTSP_PORT", 554),
			RTMPPort:  envInt("EASYAVR_ZLM_RTMP_PORT", 1935),
			HTTPPort:  envInt("EASYAVR_ZLM_HTTP_PORT", 80),
			WSPort:    envInt("EASYAVR_ZLM_WS_PORT", 80),
		},
	}
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
