package target

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the Bitbucket Cloud 2.0 REST API.
type Client struct {
	workspace string
	email     string
	apiToken  string
	baseURL   string
	http      *http.Client
}

func NewClient(workspace, email, apiToken string) *Client {
	return &Client{
		workspace: workspace,
		email:     email,
		apiToken:  apiToken,
		baseURL:   "https://api.bitbucket.org/2.0",
		http: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *Client) Workspace() string { return c.workspace }

// RepoURL returns the HTTPS git URL without credentials.
func (c *Client) RepoURL(slug string) string {
	return fmt.Sprintf("https://bitbucket.org/%s/%s.git", c.workspace, slug)
}

type User struct {
	UUID     string `json:"uuid"`
	Username string `json:"username"`
}

func (c *Client) GetUser(ctx context.Context) (User, error) {
	var u User
	if err := c.do(ctx, http.MethodGet, "/user", nil, &u); err != nil {
		return User{}, err
	}
	return u, nil
}

// ProjectExists reports whether a Cloud project with the given key exists.
func (c *Client) ProjectExists(ctx context.Context, key string) (bool, error) {
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/workspaces/%s/projects/%s", c.workspace, key), nil, nil)
	if err == nil {
		return true, nil
	}
	if e, ok := err.(*Error); ok && e.Status == http.StatusNotFound {
		return false, nil
	}
	return false, err
}

// CreateProject creates a Cloud project. A pre-existing project is not an error:
// Bitbucket Cloud signals that with either HTTP 409 or HTTP 400 with an
// "already exists" message.
func (c *Client) CreateProject(ctx context.Context, key, name string) error {
	body := map[string]any{"key": key, "name": name, "is_private": true}
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/workspaces/%s/projects", c.workspace), body, nil)
	if e, ok := err.(*Error); ok {
		if e.Status == http.StatusConflict {
			return nil
		}
		if e.Status == http.StatusBadRequest && strings.Contains(e.Body, "already exists") {
			return nil
		}
	}
	return err
}

// RepoExists reports whether the repository already exists in the workspace.
func (c *Client) RepoExists(ctx context.Context, slug string) (bool, error) {
	err := c.do(ctx, http.MethodGet, c.repoPath(slug), nil, nil)
	if err == nil {
		return true, nil
	}
	if e, ok := err.(*Error); ok && e.Status == http.StatusNotFound {
		return false, nil
	}
	return false, err
}

type CreateRepoRequest struct {
	Slug        string
	Description string
	ProjectKey  string
	Private     bool
}

func (c *Client) CreateRepo(ctx context.Context, req CreateRepoRequest) error {
	body := map[string]any{
		"scm":        "git",
		"is_private": req.Private,
	}
	if req.Description != "" {
		body["description"] = req.Description
	}
	if req.ProjectKey != "" {
		body["project"] = map[string]any{"key": req.ProjectKey}
	}
	return c.do(ctx, http.MethodPost, c.repoPath(req.Slug), body, nil)
}

// SetMainBranch sets the repository's main branch (branch name without refs/heads/).
func (c *Client) SetMainBranch(ctx context.Context, slug, branch string) error {
	if branch == "" {
		return nil
	}
	body := map[string]any{"mainbranch": map[string]any{"name": branch}}
	return c.do(ctx, http.MethodPut, c.repoPath(slug), body, nil)
}

func (c *Client) DeleteRepo(ctx context.Context, slug string) error {
	return c.do(ctx, http.MethodDelete, c.repoPath(slug), nil, nil)
}

func (c *Client) repoPath(slug string) string {
	return fmt.Sprintf("/repositories/%s/%s", c.workspace, slug)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(c.email, c.apiToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{Status: resp.StatusCode, Method: method, Path: path, Body: strings.TrimSpace(string(raw))}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode %s %s: %w", method, path, err)
		}
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
