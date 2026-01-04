package main

import (
	"github.com/BurntSushi/toml"
	"github.com/mdobak/go-xerrors"
)

type Config struct {
	Web      WebConfig   `toml:"web"`
	Database string      `toml:"database"`
	Admin    AdminConfig `toml:"admin"`
	Routes   []Route     `toml:"routes"`
}

type WebConfig struct {
	Enabled bool   `toml:"enabled"`
	Url     string `toml:"url"`
}

type AdminConfig struct {
	Enabled bool   `toml:"enabled"`
	Url     string `toml:"url"`
}

type Route struct {
	Url    string `toml:"url"`
	Target string `toml:"target"`
	Tls    bool   `toml:"tls"`
}

func loadConfig(path string) (*Config, error) {
	var cfg Config

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, xerrors.Newf("decode config: %w", err)
	}

	if cfg.Web.Url == "" {
		cfg.Web.Url = "localhost:8080"
	}
	if cfg.Database == "" {
		cfg.Database = "./remazarin.db"
	}
	if cfg.Admin.Url == "" {
		cfg.Admin.Url = "localhost:8081"
	}

	return &cfg, nil
}
