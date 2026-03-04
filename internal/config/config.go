package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Exchange ExchangeConfig `yaml:"exchange"`
	Bot      BotConfig      `yaml:"bot"`
	Runtime  RuntimeConfig  `yaml:"runtime"`
}

type ExchangeConfig struct {
	BaseURL string `yaml:"base_url"`
	WSURL   string `yaml:"ws_url"`
	APIKey  string `yaml:"api_key"`
	Secret  string `yaml:"secret"`
}

type BotConfig struct {
	Symbol           string  `yaml:"symbol"`
	Side             string  `yaml:"side"` // BUY or SELL
	BaseOrderQty     float64 `yaml:"base_order_qty"`
	TpPercent        float64 `yaml:"tp_percent"`
	SOCount          int     `yaml:"so_count"`
	SOStepPercent    float64 `yaml:"so_step_percent"`
	SOStepMultiplier float64 `yaml:"so_step_multiplier"`
	SOBaseQty        float64 `yaml:"so_base_qty"`
	SOQtyMultiplier  float64 `yaml:"so_qty_multiplier"`
}

type RuntimeConfig struct {
	DryRun   bool   `yaml:"dry_run"`
	LogLevel string `yaml:"log_level"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Override with env vars
	if key := os.Getenv("BYBIT_API_KEY"); key != "" {
		cfg.Exchange.APIKey = key
	}
	if secret := os.Getenv("BYBIT_API_SECRET"); secret != "" {
		cfg.Exchange.Secret = secret
	}
	return &cfg, nil
}
