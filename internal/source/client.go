package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to the Bitbucket Server / Data Center 1.0 REST API.
type Client struct {
	baseURL  string
	user     string
	token    string
	password string
	http     *http.Client
}

func NewClient(baseURL, user, token, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		user:     user,
		token:    token,
		password: password,
		http: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type Project struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Public bool   `json:"public"`
}

type Repository struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Public   bool   `json:"public"`
	Archived bool   `json:"archived"`
	// DefaultBranch is reported as a full ref, e.g. "refs/heads/master".
	DefaultBranch string `json:"defaultBranch"`
	State         string `json:"state"`
	Size          int64  `json:"size"`
	Project       struct {
		Key string `json:"key"`
	} `json:"project"`
	Links struct {
		Clone []struct {
			Href string `json:"href"`
			Name string `json:"name"`
		} `json:"clone"`
	} `json:"links"`
}

// CloneURL returns the HTTP(S) clone URL without embedded credentials.
func (r Repository) CloneURL(baseURL string) string {
	for _, l := range r.Links.Clone {
		if l.Name == "http" || l.Name == "https" {
			return l.Href
		}
	}
	project := strings.ToLower(r.Project.Key)
	return fmt.Sprintf("%s/scm/%s/%s.git", strings.TrimRight(baseURL, "/"), project, r.Slug)
}

// DefaultBranchName returns the branch name without the refs/heads/ prefix.
func (r Repository) DefaultBranchName() string {
	return strings.TrimPrefix(r.DefaultBranch, "refs/heads/")
}

type page[T any] struct {
	Size          int  `json:"size"`
	Limit         int  `json:"limit"`
	IsLastPage    bool `json:"isLastPage"`
	Start         int  `json:"start"`
	NextPageStart int  `json:"nextPageStart"`
	Values        []T  `json:"values"`
}

func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var all []Project
	start := 0
	for {
		var p page[Project]
		if err := c.get(ctx, fmt.Sprintf("/rest/api/1.0/projects?limit=1000&start=%d", start), &p); err != nil {
			return nil, err
		}
		all = append(all, p.Values...)
		if p.IsLastPage || len(p.Values) == 0 {
			return all, nil
		}
		start = p.NextPageStart
	}
}

func (c *Client) ListRepos(ctx context.Context, projectKey string) ([]Repository, error) {
	var all []Repository
	start := 0
	for {
		path := fmt.Sprintf("/rest/api/1.0/projects/%s/repos?limit=1000&start=%d", url.PathEscape(projectKey), start)
		var p page[Repository]
		if err := c.get(ctx, path, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Values...)
		if p.IsLastPage || len(p.Values) == 0 {
			return all, nil
		}
		start = p.NextPageStart
	}
}

func (c *Client) GetRepo(ctx context.Context, projectKey, slug string) (Repository, error) {
	var r Repository
	path := fmt.Sprintf("/rest/api/1.0/projects/%s/repos/%s", url.PathEscape(projectKey), url.PathEscape(slug))
	if err := c.get(ctx, path, &r); err != nil {
		return Repository{}, err
	}
	return r, nil
}

// Ping verifies connectivity and credentials with a minimal authenticated
// request. It reports the round-trip latency, which is still useful when the
// request fails.
func (c *Client) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	var p page[struct{}]
	if err := c.get(ctx, "/rest/api/1.0/projects?limit=1", &p); err != nil {
		return time.Since(start), err
	}
	return time.Since(start), nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		req.SetBasicAuth(c.user, c.password)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{Status: resp.StatusCode, Method: http.MethodGet, Path: path, Body: strings.TrimSpace(string(body))}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

type Error struct {
	Status int
	Method string
	Path   string
	Body   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.Status, e.Body)
}
