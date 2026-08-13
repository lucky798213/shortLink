package config

import (
	"errors"
	"fmt"
)

type Web struct {
	HTTP struct {
		Addr string `mapstructure:"addr"`
	} `mapstructure:"http"`
	GRPC struct {
		Addr string `mapstructure:"addr"`
	} `mapstructure:"grpc"`
	ShortURL struct {
		BaseURL string `mapstructure:"base_url"`
	} `mapstructure:"short_url"`
	Redis     Redis `mapstructure:"redis"`
	RateLimit struct {
		APIRate       float64 `mapstructure:"api_rate"`
		APIBurst      int     `mapstructure:"api_burst"`
		RedirectRate  float64 `mapstructure:"redirect_rate"`
		RedirectBurst int     `mapstructure:"redirect_burst"`
	} `mapstructure:"rate_limit"`
	CircuitBreaker struct {
		TimeoutMS              int `mapstructure:"timeout_ms"`
		MaxConcurrent          int `mapstructure:"max_concurrent"`
		RequestVolumeThreshold int `mapstructure:"request_volume_threshold"`
		SleepWindowMS          int `mapstructure:"sleep_window_ms"`
		ErrorPercentThreshold  int `mapstructure:"error_percent_threshold"`
	} `mapstructure:"circuit_breaker"`
	Auth struct {
		Enabled bool   `mapstructure:"enabled"`
		APIKey  string `mapstructure:"api_key"`
	} `mapstructure:"auth"`
	Etcd    Etcd `mapstructure:"etcd"`
	Service struct {
		Name string `mapstructure:"name"`
	} `mapstructure:"service"`
	Logger Logger `mapstructure:"logger"`
}

func LoadWeb(configFile string) (Web, error) {
	v, err := newViper("web", configFile)
	if err != nil {
		return Web{}, err
	}
	setWebDefaults(v)
	var cfg Web
	if err := v.Unmarshal(&cfg); err != nil {
		return Web{}, fmt.Errorf("解析 Web 配置: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Web{}, err
	}
	return cfg, nil
}

func (c Web) Validate() error {
	if c.HTTP.Addr == "" {
		return errors.New("Web 配置 http.addr 不能为空")
	}
	if c.GRPC.Addr == "" {
		return errors.New("Web 配置 grpc.addr 不能为空")
	}
	if c.ShortURL.BaseURL == "" {
		return errors.New("Web 配置 short_url.base_url 不能为空")
	}
	if c.Auth.Enabled && c.Auth.APIKey == "" {
		return errors.New("启用 API 鉴权时 auth.api_key 不能为空")
	}
	return nil
}

func setWebDefaults(v interface{ SetDefault(string, any) }) {
	v.SetDefault("http.addr", ":8080")
	v.SetDefault("grpc.addr", "127.0.0.1:50051")
	v.SetDefault("short_url.base_url", "http://localhost:8080")
	v.SetDefault("redis.addr", "127.0.0.1:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("rate_limit.api_rate", 50.0)
	v.SetDefault("rate_limit.api_burst", 100)
	v.SetDefault("rate_limit.redirect_rate", 500.0)
	v.SetDefault("rate_limit.redirect_burst", 1000)
	v.SetDefault("auth.enabled", false)
	v.SetDefault("auth.api_key", "")
	v.SetDefault("etcd.enabled", false)
	v.SetDefault("etcd.endpoints", []string{"127.0.0.1:2379"})
	v.SetDefault("etcd.dial_timeout", "3s")
	v.SetDefault("service.name", "short-url-rpc")
	v.SetDefault("logger.level", "info")
	v.SetDefault("logger.development", false)
	v.SetDefault("circuit_breaker.timeout_ms", 2500)
	v.SetDefault("circuit_breaker.max_concurrent", 100)
	v.SetDefault("circuit_breaker.request_volume_threshold", 20)
	v.SetDefault("circuit_breaker.sleep_window_ms", 5000)
	v.SetDefault("circuit_breaker.error_percent_threshold", 50)
}
