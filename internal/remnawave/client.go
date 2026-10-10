package remnawave

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	BaseURL     string
	Token       string
	Headers     map[string]string
	InsecureTLS bool
	UserAgent   string
}

type Client struct {
	cfg  Config
	base string
	http *http.Client
}

type APIError struct {
	Status    int
	Message   string
	ErrorCode string
}

func (e *APIError) Error() string {
	if e.ErrorCode != "" {
		return fmt.Sprintf("remnawave api: %d %s (%s)", e.Status, e.Message, e.ErrorCode)
	}
	return fmt.Sprintf("remnawave api: %d %s", e.Status, e.Message)
}

func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && (apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusMethodNotAllowed)
}

// NormalizeBaseURL trims trailing slashes and an accidental "/api" suffix.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("panel URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid panel URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("panel URL must start with http:// or https://")
	}
	if u.Host == "" {
		return "", errors.New("panel URL has no host")
	}
	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/api")
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func New(cfg Config) (*Client, error) {
	base, err := NormalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("panel API token is empty")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.InsecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in for self-signed panels
	}
	return &Client{
		cfg:  cfg,
		base: base,
		http: &http.Client{Timeout: 20 * time.Second, Transport: transport},
	}, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.cfg.Token))
	req.Header.Set("Accept", "application/json")
	// The panel refuses plain-HTTP requests that did not pass through a reverse
	// proxy. These headers make direct access over the docker network work.
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("X-Forwarded-Proto", "https")
	if c.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.cfg.Headers {
		if strings.TrimSpace(k) != "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		var parsed struct {
			Message   any    `json:"message"`
			ErrorCode string `json:"errorCode"`
		}
		if json.Unmarshal(raw, &parsed) == nil {
			switch m := parsed.Message.(type) {
			case string:
				apiErr.Message = m
			case []any:
				parts := make([]string, 0, len(m))
				for _, p := range m {
					parts = append(parts, fmt.Sprint(p))
				}
				apiErr.Message = strings.Join(parts, "; ")
			}
			apiErr.ErrorCode = parsed.ErrorCode
		}
		return apiErr
	}

	if out == nil {
		return nil
	}
	envelope := struct {
		Response json.RawMessage `json:"response"`
	}{}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode response from %s: %w", path, err)
	}
	if len(envelope.Response) == 0 {
		return fmt.Errorf("unexpected response from %s: missing \"response\" field (is the URL pointing to the panel?)", path)
	}
	if err := json.Unmarshal(envelope.Response, out); err != nil {
		return fmt.Errorf("decode response from %s: %w", path, err)
	}
	return nil
}

func (c *Client) Metadata(ctx context.Context) (*Metadata, error) {
	var m Metadata
	if err := c.do(ctx, http.MethodGet, "/api/system/metadata", nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var nodes []Node
	if err := c.do(ctx, http.MethodGet, "/api/nodes", nil, &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (c *Client) Hosts(ctx context.Context) ([]Host, error) {
	var hosts []Host
	if err := c.do(ctx, http.MethodGet, "/api/hosts", nil, &hosts); err != nil {
		return nil, err
	}
	return hosts, nil
}

// SetHostsDisabled uses the bulk endpoints available in every supported panel
// version and falls back to per-host PATCH if they are missing.
func (c *Client) SetHostsDisabled(ctx context.Context, uuids []string, disabled bool) error {
	if len(uuids) == 0 {
		return nil
	}
	path := "/api/hosts/bulk/enable"
	if disabled {
		path = "/api/hosts/bulk/disable"
	}
	err := c.do(ctx, http.MethodPost, path, map[string]any{"uuids": uuids}, nil)
	if err == nil || !IsNotFound(err) {
		return err
	}
	for _, id := range uuids {
		if err := c.do(ctx, http.MethodPatch, "/api/hosts", map[string]any{"uuid": id, "isDisabled": disabled}, nil); err != nil {
			return fmt.Errorf("host %s: %w", id, err)
		}
	}
	return nil
}

func (c *Client) Templates(ctx context.Context) ([]SubscriptionTemplate, error) {
	var out struct {
		Templates []SubscriptionTemplate `json:"templates"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/subscription-templates", nil, &out); err != nil {
		return nil, err
	}
	return out.Templates, nil
}

func (c *Client) Template(ctx context.Context, uuid string) (*SubscriptionTemplate, error) {
	var t SubscriptionTemplate
	if err := c.do(ctx, http.MethodGet, "/api/subscription-templates/"+url.PathEscape(uuid), nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// TemplateYAML returns the decoded YAML body of a subscription template.
func (c *Client) TemplateYAML(ctx context.Context, uuid string) (*SubscriptionTemplate, string, error) {
	t, err := c.Template(ctx, uuid)
	if err != nil {
		return nil, "", err
	}
	if t.EncodedTemplateYAML == nil {
		return t, "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(*t.EncodedTemplateYAML)
	if err != nil {
		return nil, "", fmt.Errorf("decode template %s: %w", uuid, err)
	}
	return t, string(raw), nil
}

func (c *Client) UpdateTemplateYAML(ctx context.Context, uuid, yaml string) error {
	body := map[string]any{
		"uuid":                uuid,
		"encodedTemplateYaml": base64.StdEncoding.EncodeToString([]byte(yaml)),
	}
	return c.do(ctx, http.MethodPatch, "/api/subscription-templates", body, nil)
}
