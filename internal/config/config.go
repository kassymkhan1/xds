// Package config loads the service inventory that the xDS server turns into
// Envoy resources (see internal/xds.BuildSnapshot).
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Endpoint is a single upstream host:port backing a Service.
type Endpoint struct {
	Host string `yaml:"host"`
	Port uint32 `yaml:"port"`
}

// Service describes one Envoy listener, the cluster it routes all traffic
// to, and that cluster's upstream endpoints.
type Service struct {
	Name         string     `yaml:"name"`
	ListenerPort uint32     `yaml:"listener_port"`
	Endpoints    []Endpoint `yaml:"endpoints"`
}

// Config is the top-level shape of the config file.
type Config struct {
	Services []Service `yaml:"services"`
}

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %q: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %q: %w", path, err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if len(c.Services) == 0 {
		return fmt.Errorf("no services defined")
	}

	seen := make(map[string]bool, len(c.Services))
	for _, svc := range c.Services {
		if svc.Name == "" {
			return fmt.Errorf("service missing name")
		}
		if seen[svc.Name] {
			return fmt.Errorf("duplicate service name %q", svc.Name)
		}
		seen[svc.Name] = true

		if svc.ListenerPort == 0 {
			return fmt.Errorf("service %q: missing listener_port", svc.Name)
		}
		if len(svc.Endpoints) == 0 {
			return fmt.Errorf("service %q: no endpoints", svc.Name)
		}
		for _, ep := range svc.Endpoints {
			if ep.Host == "" || ep.Port == 0 {
				return fmt.Errorf("service %q: endpoint missing host or port", svc.Name)
			}
		}
	}

	return nil
}
