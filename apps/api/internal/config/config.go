// Package config loads process configuration without implicitly reading files.
package config

import (
	"errors"
	"net"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	ShutdownTimeout time.Duration
}

func Load() (Config, error) {
	c := Config{HTTPAddr: os.Getenv("HTTP_ADDR"), DatabaseURL: os.Getenv("DATABASE_URL"), ShutdownTimeout: 15 * time.Second}
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
	return c, nil
}
