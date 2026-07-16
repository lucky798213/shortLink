package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Logger struct {
	Level       string `mapstructure:"level"`
	Development bool   `mapstructure:"development"`
}

type Redis struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type Etcd struct {
	Enabled     bool          `mapstructure:"enabled"`
	Endpoints   []string      `mapstructure:"endpoints"`
	DialTimeout time.Duration `mapstructure:"dial_timeout"`
	LeaseTTL    int64         `mapstructure:"lease_ttl"`
}

func newViper(name string, configFile string) (*viper.Viper, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.SetConfigName(name)
		v.AddConfigPath("./configs")
		v.AddConfigPath(".")
		v.AddConfigPath("/etc/short_url")
	}
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取 %s 配置: %w", name, err)
	}
	return v, nil
}
