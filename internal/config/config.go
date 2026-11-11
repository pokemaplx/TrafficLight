// Package config reads config.toml and the TRAFFICLIGHT_* environment variables
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/pokemaplx/TrafficLight/internal/protos"
)

const envPrefix = "TRAFFICLIGHT_"

type Config struct {
	// Host and Port of the receiver, where mitms send their traffic
	Host string
	Port int

	// every request the receiver gets is passed on to this url too, i.e. a Golbat's /raw.
	// Empty: don't forward
	ForwardURL   string
	ForwardToken string

	// Output is "web" or "print"
	Output string

	WebHost        string
	WebPort        int
	WebOpenBrowser bool
	// the web UI's log holds this many requests at most, and never more than WebMaxMemoryMB
	// of them. Whichever is reached first drops the oldest. 0 turns the memory limit off
	WebMaxRecords  int
	WebMaxMemoryMB int
	// empty: no login
	WebPassword string

	// ProtosURL is where newer protos are looked for while Traffic Light runs, every
	// ProtosUpdateHours and once at startup. Empty: the built in ones are all there is.
	// 0 hours: only at startup
	ProtosURL         string
	ProtosUpdateHours int
	// MethodsURL is a methods file, a url or a path: names for methods the protos don't
	// know yet. Empty: none
	MethodsURL string

	// DatabaseURL names a MariaDB or MySQL database that keeps saved requests past a restart,
	// i.e. mariadb://user:password@host:3306/database. Empty: they live in memory only
	DatabaseURL string

	// DebugAddr serves Go's profiler there, i.e. 127.0.0.1:6060, to see where memory and
	// time go. Empty: off
	DebugAddr string
}

func defaults() *Config {
	return &Config{
		Host:           "0.0.0.0",
		Port:           3335,
		Output:         "web",
		WebHost:        "127.0.0.1",
		WebPort:        3336,
		WebOpenBrowser: true,
		WebMaxRecords:  5000,
		WebMaxMemoryMB: 128,

		ProtosURL:         protos.UpstreamProtos,
		ProtosUpdateHours: 6,
	}
}

// Load reads the config file, which doesn't have to exist, and then the environment, which
// wins over the file
func Load(path string) (*Config, error) {
	config := defaults()

	var file map[string]any
	if _, err := toml.DecodeFile(path, &file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for key, value := range file {
		if err := config.set(strings.ToLower(key), fmt.Sprint(value)); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}

	for _, variable := range os.Environ() {
		name, value, _ := strings.Cut(variable, "=")
		if len(name) <= len(envPrefix) || !strings.EqualFold(name[:len(envPrefix)], envPrefix) {
			continue
		}
		if err := config.set(strings.ToLower(name[len(envPrefix):]), value); err != nil {
			return nil, fmt.Errorf("%s: %w", strings.ToUpper(name), err)
		}
	}

	return config, config.validate()
}

// set takes every value as text, so a number in quotes or an environment variable work alike.
// Options that don't exist (anymore) are skipped
func (c *Config) set(key string, value string) error {
	var err error
	switch key {
	case "host":
		c.Host = value
	case "port":
		c.Port, err = integer(key, value)
	case "forward_url":
		c.ForwardURL = strings.TrimSpace(value)
	case "forward_token":
		c.ForwardToken = value
	case "output":
		c.Output = strings.ToLower(strings.TrimSpace(value))
	case "web_host":
		c.WebHost = value
	case "web_port":
		c.WebPort, err = integer(key, value)
	case "web_open_browser":
		c.WebOpenBrowser, err = boolean(key, value)
	case "web_max_records":
		c.WebMaxRecords, err = integer(key, value)
	case "web_max_memory_mb":
		c.WebMaxMemoryMB, err = integer(key, value)
	case "web_password":
		c.WebPassword = value
	case "protos_url":
		c.ProtosURL = strings.TrimSpace(value)
	case "protos_update_hours":
		c.ProtosUpdateHours, err = integer(key, value)
	case "methods_url":
		c.MethodsURL = strings.TrimSpace(value)
	case "database_url":
		c.DatabaseURL = strings.TrimSpace(value)
	case "debug_addr":
		c.DebugAddr = strings.TrimSpace(value)
	}
	return err
}

func (c *Config) validate() error {
	switch c.Output {
	case "web", "print":
	case "ui", "discord":
		return fmt.Errorf("output = %q is gone: this version of Traffic Light only has \"web\" and \"print\"", c.Output)
	default:
		return fmt.Errorf("output = %q doesn't exist, use \"web\" or \"print\"", c.Output)
	}

	for name, port := range map[string]int{"port": c.Port, "web_port": c.WebPort} {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%s = %d is not a port", name, port)
		}
	}
	if c.WebMaxRecords < 1 {
		return fmt.Errorf("web_max_records = %d has to be at least 1", c.WebMaxRecords)
	}
	if c.ProtosUpdateHours < 0 {
		return fmt.Errorf("protos_update_hours = %d can't be negative, 0 looks for new protos at startup only", c.ProtosUpdateHours)
	}
	if c.WebMaxMemoryMB < 0 {
		return fmt.Errorf("web_max_memory_mb = %d can't be negative, 0 turns the limit off", c.WebMaxMemoryMB)
	}
	return nil
}

func integer(key, value string) (int, error) {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s = %q is not a number", key, value)
	}
	return number, nil
}

func boolean(key, value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "y", "t":
		return true, nil
	case "0", "false", "no", "off", "n", "f":
		return false, nil
	}
	return false, fmt.Errorf("%s = %q is neither true nor false", key, value)
}
