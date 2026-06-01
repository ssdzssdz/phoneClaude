package config

import (
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Auth    AuthConfig    `yaml:"auth"`
	Session SessionConfig `yaml:"session"`
	Claude  ClaudeConfig  `yaml:"claude"`
	Notify  NotifyConfig  `yaml:"notify"`
}

type ServerConfig struct {
	ListenTailscale string `yaml:"listen_tailscale"`
	ListenLocal     string `yaml:"listen_local"`
}

type AuthConfig struct {
	JWTSecret string `yaml:"jwt_secret"`
	JWTTTL    string `yaml:"jwt_ttl"`
	PinTTL    string `yaml:"pin_ttl"`
}

type SessionConfig struct {
	MaxSessions    int    `yaml:"max_sessions"`
	IdleTimeout    string `yaml:"idle_timeout"`
	WorkDirDefault string `yaml:"work_dir_default"`
}

type ClaudeConfig struct {
	Binary string            `yaml:"binary"`
	Env    map[string]string `yaml:"env"`
}

type NotifyConfig struct {
	FCMEnabled bool `yaml:"fcm_enabled"`
}

var (
	instance *Config
	once     sync.Once
)

func Load(path string) (*Config, error) {
	var loadErr error
	once.Do(func() {
		instance = &Config{
			Server: ServerConfig{
				ListenTailscale: "100.64.0.1:9527",
				ListenLocal:     "127.0.0.1:9527",
			},
			Auth: AuthConfig{
				JWTSecret: "",
				JWTTTL:    "720h",
				PinTTL:    "5m",
			},
			Session: SessionConfig{
				MaxSessions:    10,
				IdleTimeout:    "30m",
				WorkDirDefault: ".",
			},
			Claude: ClaudeConfig{
				Binary: "claude",
			},
			Notify: NotifyConfig{
				FCMEnabled: true,
			},
		}

		if path == "" {
			path = "phone-claude-bridge.yaml"
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				loadErr = nil
				return
			}
			loadErr = err
			return
		}
		loadErr = yaml.Unmarshal(data, instance)
	})
	return instance, loadErr
}

func Get() *Config {
	if instance == nil {
		panic("config not loaded, call Load() first")
	}
	return instance
}
