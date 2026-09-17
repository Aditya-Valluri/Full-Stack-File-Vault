// Package config loads process configuration without implicitly reading files.
package config

import (
	"balkanid.local/vault/api/internal/auth"
	"errors"
	"net"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr                    string
	DatabaseURL                 string
	ShutdownTimeout             time.Duration
	Browser                     auth.BrowserConfig
	BootstrapCreationsPerMinute int
}

func Load() (Config, error) {
	c := Config{HTTPAddr: os.Getenv("HTTP_ADDR"), DatabaseURL: os.Getenv("DATABASE_URL"), ShutdownTimeout: 15 * time.Second}
	c.BootstrapCreationsPerMinute = 60
	if value := os.Getenv("BOOTSTRAP_CREATIONS_PER_MINUTE"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 600 {
			return Config{}, errors.New("BOOTSTRAP_CREATIONS_PER_MINUTE must be between 1 and 600")
		}
		c.BootstrapCreationsPerMinute = limit
	}
	if c.HTTPAddr == "" {
		c.HTTPAddr = "127.0.0.1:8080"
	}
	if c.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	_, port, err := net.SplitHostPort(c.HTTPAddr)
	if err != nil {
		return Config{}, errors.New("HTTP_ADDR must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return Config{}, errors.New("HTTP_ADDR requires a numeric port between 1 and 65535")
	}
	environment := os.Getenv("APP_ENV")
	if environment == "" {
		environment = "production"
	}
	if environment != "production" && environment != "development" {
		return Config{}, errors.New("APP_ENV must be production or development")
	}
	c.Browser = auth.BrowserConfig{Origin: os.Getenv("PUBLIC_ORIGIN"), Development: environment == "development"}
	if err := auth.ValidateBrowserConfig(c.Browser); err != nil {
		return Config{}, err
	}
	if c.Browser.Development && len(c.Browser.Origin) >= 7 && c.Browser.Origin[:7] == "http://" {
		host, _, _ := net.SplitHostPort(c.HTTPAddr)
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return Config{}, errors.New("HTTP development requires a loopback HTTP_ADDR")
		}
	}
	return c, nil
}
