package main

import (
	"github.com/BurntSushi/toml"
	"github.com/mdobak/go-xerrors"
)

type Config struct {
	Web      WebConfig   `toml:"web"`
	Database string      `toml:"database"`
	Admin    AdminConfig `toml:"admin"`
	Otel     OtelConfig  `toml:"otel"`
	Routes   []Route     `toml:"routes"`
}

type WebConfig struct {
	Enabled bool   `toml:"enabled"`
	Url     string `toml:"url"`
	Target  string `toml:"target"`
	Tls     bool   `toml:"tls"`
	Cert    string `toml:"cert"`
	Key     string `toml:"key"`
}

type AdminConfig struct {
	Enabled bool   `toml:"enabled"`
	Url     string `toml:"url"`
	Target  string `toml:"target"`
	Tls     bool   `toml:"tls"`
	Cert    string `toml:"cert"`
	Key     string `toml:"key"`
}

type OtelConfig struct {
	Enabled  bool   `toml:"enabled"`
	Endpoint string `toml:"endpoint"`
	Interval int    `toml:"interval"`
}

type Route struct {
	Url    string `toml:"url"`
	Target string `toml:"target"`
	Type   string `toml:"type"`
	Tls    bool   `toml:"tls"`
	Cert   string `toml:"cert"`
	Key    string `toml:"key"`
}

func loadConfig(path string) (*Config, error) {
	var cfg Config

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, xerrors.Newf("decode config: %w", err)
	}

	validateConfig(&cfg)
	generateAuthAdm(&cfg)

	return &cfg, nil
}

func validateConfig(cfg *Config) {
	if cfg.Web.Url == "" {
		cfg.Web.Url = "localhost:8080"
	}
	if cfg.Web.Target == "" {
		cfg.Web.Target = "./www/auth"
	}

	if cfg.Database == "" {
		cfg.Database = "./remazarin.db"
	}

	if cfg.Admin.Url == "" {
		cfg.Admin.Url = "localhost:8081"
	}
	if cfg.Admin.Target == "" {
		cfg.Admin.Target = "./www/admin"
	}
}

func generateAuthAdm(cfg *Config) {
	if cfg.Web.Enabled {
		webRoute := Route{
			Url:    cfg.Web.Url,
			Target: cfg.Web.Target,
			Type:   "static",
			Tls:    cfg.Web.Tls,
			Cert:   cfg.Web.Cert,
			Key:    cfg.Web.Key,
		}
		cfg.Routes = append(cfg.Routes, webRoute)
	}

	if cfg.Admin.Enabled {
		admRoute := Route{
			Url:    cfg.Admin.Url,
			Target: cfg.Admin.Target,
			Type:   "static",
			Tls:    cfg.Admin.Tls,
			Cert:   cfg.Admin.Cert,
			Key:    cfg.Admin.Key,
		}
		cfg.Routes = append(cfg.Routes, admRoute)
	}
}
