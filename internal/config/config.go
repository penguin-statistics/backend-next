package config

import (
	"os"
	"strconv"
)

type Config struct {
	// The port to listen on.
	Port    int  `json:"port"`
	DevMode bool `json:"dev"`
}

func Parse() *Config {
	portStr := os.Getenv("PENGUIN_V4_PORT")
	if portStr == "" {
		portStr = "9010"
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		panic(err)
	}

	devMode := os.Getenv("PENGUIN_V4_DEV") == "true"

	return &Config{
		Port:    port,
		DevMode: devMode,
	}
}
