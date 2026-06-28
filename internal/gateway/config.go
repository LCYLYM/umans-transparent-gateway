package gateway

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type SearchMode string

const (
	SearchAuto   SearchMode = "auto"
	SearchNative SearchMode = "native"
	SearchExa    SearchMode = "exa"
	SearchNone   SearchMode = "none"
)

type BudgetPolicy string

const (
	BudgetError        BudgetPolicy = "error"
	BudgetClampVisible BudgetPolicy = "clamp-visible"
)

type Config struct {
	Listen            string
	UpstreamBaseURL   string
	DefaultModel      string
	OpusModel         string
	SonnetModel       string
	HaikuModel        string
	KeyConcurrency    int
	KeyQueueTimeout   time.Duration
	UpstreamRetryMax  int
	UpstreamRetryBase time.Duration
	UpstreamRetryCap  time.Duration
	SchemaCompat      bool
	CatalogTTL        time.Duration
	SearchMode        SearchMode
	BudgetPolicy      BudgetPolicy
	ErrorEventDir     string
	ErrorEventMaxAge  time.Duration
	ErrorEventMaxSize int64
}

func DefaultConfig() Config {
	return Config{
		Listen:            "0.0.0.0:8080",
		UpstreamBaseURL:   "https://api.code.umans.ai",
		DefaultModel:      "umans-coder",
		OpusModel:         "umans-glm-5.2",
		SonnetModel:       "umans-coder",
		HaikuModel:        "umans-flash",
		KeyConcurrency:    4,
		KeyQueueTimeout:   10 * time.Minute,
		UpstreamRetryMax:  2,
		UpstreamRetryBase: 2 * time.Second,
		UpstreamRetryCap:  5 * time.Second,
		SchemaCompat:      true,
		CatalogTTL:        10 * time.Minute,
		SearchMode:        SearchExa,
		BudgetPolicy:      BudgetError,
		ErrorEventDir:     "error-events",
		ErrorEventMaxAge:  24 * time.Hour,
		ErrorEventMaxSize: 5 * 1024 * 1024,
	}
}

func ConfigFromEnv() Config {
	cfg := DefaultConfig()
	cfg.Listen = envString("UMANS_GATEWAY_LISTEN", cfg.Listen)
	cfg.UpstreamBaseURL = envString("UMANS_UPSTREAM_BASE_URL", cfg.UpstreamBaseURL)
	cfg.DefaultModel = envString("UMANS_DEFAULT_MODEL", cfg.DefaultModel)
	cfg.OpusModel = envString("UMANS_OPUS_MODEL", cfg.OpusModel)
	cfg.SonnetModel = envString("UMANS_SONNET_MODEL", cfg.SonnetModel)
	cfg.HaikuModel = envString("UMANS_HAIKU_MODEL", cfg.HaikuModel)
	cfg.KeyConcurrency = envInt("UMANS_KEY_CONCURRENCY_LIMIT", cfg.KeyConcurrency)
	cfg.KeyQueueTimeout = envDuration("UMANS_KEY_QUEUE_TIMEOUT", cfg.KeyQueueTimeout)
	cfg.UpstreamRetryMax = envNonNegativeInt("UMANS_UPSTREAM_RETRY_MAX", cfg.UpstreamRetryMax)
	cfg.UpstreamRetryBase = envDuration("UMANS_UPSTREAM_RETRY_BASE_DELAY", cfg.UpstreamRetryBase)
	cfg.UpstreamRetryCap = envDuration("UMANS_UPSTREAM_RETRY_MAX_DELAY", cfg.UpstreamRetryCap)
	cfg.SchemaCompat = envBool("UMANS_SCHEMA_COMPAT", cfg.SchemaCompat)
	cfg.ErrorEventDir = envString("UMANS_ERROR_EVENT_DIR", cfg.ErrorEventDir)
	cfg.SearchMode = SearchMode(envString("UMANS_SEARCH_MODE", string(cfg.SearchMode)))
	cfg.BudgetPolicy = BudgetPolicy(envString("UMANS_BUDGET_POLICY", string(cfg.BudgetPolicy)))
	cfg.CatalogTTL = envDuration("UMANS_CATALOG_TTL", cfg.CatalogTTL)
	cfg.ErrorEventMaxAge = envDuration("UMANS_ERROR_EVENT_MAX_AGE", cfg.ErrorEventMaxAge)
	cfg.ErrorEventMaxSize = envBytes("UMANS_ERROR_EVENT_MAX_SIZE", cfg.ErrorEventMaxSize)
	return cfg
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Listen) == "" {
		return errors.New("listen address is empty")
	}
	u, err := url.Parse(c.UpstreamBaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("upstream base URL must be absolute")
	}
	switch c.SearchMode {
	case SearchAuto, SearchNative, SearchExa, SearchNone:
	default:
		return errors.New("search mode must be auto, native, exa, or none")
	}
	switch c.BudgetPolicy {
	case BudgetError, BudgetClampVisible:
	default:
		return errors.New("budget policy must be error or clamp-visible")
	}
	if c.CatalogTTL <= 0 {
		return errors.New("catalog ttl must be positive")
	}
	if c.KeyConcurrency <= 0 {
		return errors.New("key concurrency limit must be positive")
	}
	if c.KeyQueueTimeout <= 0 {
		return errors.New("key queue timeout must be positive")
	}
	if c.UpstreamRetryMax < 0 {
		return errors.New("upstream retry max must be non-negative")
	}
	if c.UpstreamRetryBase <= 0 || c.UpstreamRetryCap <= 0 {
		return errors.New("upstream retry delays must be positive")
	}
	if c.ErrorEventMaxAge <= 0 || c.ErrorEventMaxSize <= 0 {
		return errors.New("error event limits must be positive")
	}
	return nil
}

func envString(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func envInt(name string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func envNonNegativeInt(name string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

func envBytes(name string, fallback int64) int64 {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
