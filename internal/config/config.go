package config

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type Source struct {
	BaseURL  string `yaml:"base_url"`
	User     string `yaml:"-"`
	Token    string `yaml:"-"`
	Password string `yaml:"-"`
}

type Target struct {
	Workspace string `yaml:"workspace"`
	Email     string `yaml:"-"`
	APIToken  string `yaml:"-"`
}

// RepoOverride re-maps one repository within a project: to a different Cloud
// project, to a different target slug, or both. An empty Destination inherits
// the project destination; an empty TargetSlug keeps the source slug.
type RepoOverride struct {
	Repo        string `yaml:"repo"`
	Destination string `yaml:"destination"`
	TargetSlug  string `yaml:"target_slug"`
}

type Project struct {
	Key             string         `yaml:"key"`
	Destination     string         `yaml:"destination"`
	Repos           []string       `yaml:"repos"`
	IncludeArchived bool           `yaml:"include_archived"`
	Overrides       []RepoOverride `yaml:"overrides"`
}

// OverrideFor returns the override configured for a source repository slug.
func (p Project) OverrideFor(slug string) (RepoOverride, bool) {
	for _, ov := range p.Overrides {
		if ov.Repo == slug {
			return ov, true
		}
	}
	return RepoOverride{}, false
}

type Options struct {
	Workers             int    `yaml:"workers"`
	TempDir             string `yaml:"temp_dir"`
	OnError             string `yaml:"on_error"`
	OnSlugCollision     string `yaml:"on_slug_collision"`
	Rollback            bool   `yaml:"rollback"`
	CreateCloudProjects bool   `yaml:"create_cloud_projects"`
}

type Config struct {
	Source   Source    `yaml:"source"`
	Target   Target    `yaml:"target"`
	Projects []Project `yaml:"projects"`
	Options  Options   `yaml:"options"`
}

// ProjectByKey returns the configured project with the given source key.
func (c *Config) ProjectByKey(key string) (Project, bool) {
	for _, p := range c.Projects {
		if p.Key == key {
			return p, true
		}
	}
	return Project{}, false
}

const (
	OnErrorContinue = "continue"
	OnErrorStop     = "stop"
)

// Load reads config from path and, when present, credentials from ./.env.
func Load(path string) (*Config, error) {
	return LoadWithEnv(path, "")
}

// LoadWithEnv reads config from path. Credentials come from envPath when set (a
// missing file is an error), otherwise from ./.env when present. Real
// environment variables always take precedence over dotenv values.
func LoadWithEnv(path, envPath string) (*Config, error) {
	if envPath == "" {
		if err := loadDotEnvAt(".env", true); err != nil {
			return nil, fmt.Errorf("read .env: %w", err)
		}
	} else if err := loadDotEnvAt(envPath, false); err != nil {
		return nil, fmt.Errorf("read env file %q: %w", envPath, err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	cfg.applyEnv()
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyEnv() {
	c.Source.BaseURL = strings.TrimSpace(c.Source.BaseURL)
	c.Source.User = os.Getenv("BITBUCKET_SERVER_USER")
	c.Source.Token = os.Getenv("BITBUCKET_SERVER_TOKEN")
	c.Source.Password = os.Getenv("BITBUCKET_SERVER_PASSWORD")

	if v := os.Getenv("BITBUCKET_CLOUD_WORKSPACE"); v != "" {
		c.Target.Workspace = v
	}
	c.Target.Email = os.Getenv("BITBUCKET_CLOUD_EMAIL")
	c.Target.APIToken = os.Getenv("BITBUCKET_CLOUD_API_TOKEN")
}

func (c *Config) applyDefaults() {
	if c.Options.Workers == 0 {
		c.Options.Workers = 3
	}
	if c.Options.OnError == "" {
		c.Options.OnError = OnErrorContinue
	}
	if c.Options.OnSlugCollision == "" {
		c.Options.OnSlugCollision = "fail"
	}
}

func (c *Config) validate() error {
	if c.Source.BaseURL == "" {
		return fmt.Errorf("source.base_url is required")
	}
	if c.Source.Token == "" && (c.Source.User == "" || c.Source.Password == "") {
		return fmt.Errorf("set BITBUCKET_SERVER_TOKEN, or BITBUCKET_SERVER_USER and BITBUCKET_SERVER_PASSWORD")
	}
	if c.Target.Workspace == "" {
		return fmt.Errorf("target.workspace is required (env BITBUCKET_CLOUD_WORKSPACE)")
	}
	if c.Target.Email == "" || c.Target.APIToken == "" {
		return fmt.Errorf("set BITBUCKET_CLOUD_EMAIL and BITBUCKET_CLOUD_API_TOKEN")
	}
	if len(c.Projects) == 0 {
		return fmt.Errorf("at least one project is required")
	}
	seen := make(map[string]bool, len(c.Projects))
	for i := range c.Projects {
		p := &c.Projects[i]
		if strings.TrimSpace(p.Key) == "" {
			return fmt.Errorf("project key must not be empty")
		}
		if seen[p.Key] {
			return fmt.Errorf("project %q listed more than once", p.Key)
		}
		seen[p.Key] = true

		dst, err := NormalizeDestination(p.Destination)
		if err != nil {
			return fmt.Errorf("project %q destination: %w", p.Key, err)
		}
		p.Destination = dst

		seenOverride := make(map[string]bool, len(p.Overrides))
		for j := range p.Overrides {
			ov := &p.Overrides[j]
			ov.Repo = strings.TrimSpace(ov.Repo)
			if ov.Repo == "" {
				return fmt.Errorf("project %q override must set repo", p.Key)
			}
			if seenOverride[ov.Repo] {
				return fmt.Errorf("project %q overrides repo %q more than once", p.Key, ov.Repo)
			}
			seenOverride[ov.Repo] = true

			ov.Destination, err = NormalizeDestination(ov.Destination)
			if err != nil {
				return fmt.Errorf("project %q override %q destination: %w", p.Key, ov.Repo, err)
			}
			ov.TargetSlug = strings.TrimSpace(ov.TargetSlug)

			if len(p.Repos) > 0 && !slices.Contains(p.Repos, ov.Repo) {
				return fmt.Errorf("project %q override repo %q is not listed in repos", p.Key, ov.Repo)
			}
		}
	}
	switch c.Options.OnError {
	case OnErrorContinue, OnErrorStop:
	default:
		return fmt.Errorf("options.on_error must be %q or %q", OnErrorContinue, OnErrorStop)
	}
	if c.Options.OnSlugCollision != "fail" {
		return fmt.Errorf("options.on_slug_collision: only \"fail\" is supported")
	}
	if c.Options.Workers < 1 {
		return fmt.Errorf("options.workers must be >= 1")
	}
	return nil
}

// NormalizeDestination upper-cases and validates an explicit Cloud project
// destination key. An empty destination is allowed and means "derive from the
// source project key".
func NormalizeDestination(dst string) (string, error) {
	dst = strings.ToUpper(strings.TrimSpace(dst))
	if dst == "" {
		return "", nil
	}
	for _, r := range dst {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return "", fmt.Errorf("%q is invalid; use only A-Z, 0-9 and _", dst)
	}
	return dst, nil
}
