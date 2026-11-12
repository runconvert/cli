// Package api is a small client for the RunConvert REST API
// (https://api.runconvert.com/v3), authenticated with an rc_live_ API key.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.runconvert.com"

type Client struct {
	BaseURL   string
	Key       string
	UserAgent string
	HTTP      *http.Client
	// Transfer is used for uploads and downloads, which can take far longer
	// than an API call; it has no overall timeout.
	Transfer *http.Client
	// ForwardIP, when set, is sent as X-Real-IP so the API rate-limits the
	// end user rather than this server. Only the remote MCP server sets it;
	// the API trusts the header only from its private network.
	ForwardIP string
}

func New(key, userAgent string) *Client {
	base := os.Getenv("RUNCONVERT_API_URL")
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{
		BaseURL:   strings.TrimRight(base, "/"),
		Key:       key,
		UserAgent: userAgent,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		Transfer:  &http.Client{},
	}
}

// Error is an API error, with the API's code (e.g. AUTH_INVALID_TOKEN).
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s (%s)", e.Message, e.Code)
	}
	return e.Message
}

// Hint gives a next step for the errors people hit most.
func (e *Error) Hint() string {
	switch {
	case e.Status == http.StatusUnauthorized:
		return "Check your API key: runconvert login, or set RUNCONVERT_API_KEY. Keys are at https://www.runconvert.com/dashboard."
	case e.Status == http.StatusPaymentRequired || strings.Contains(e.Code, "CREDIT"):
		return "Your team is out of credits. Top up at https://www.runconvert.com/pricing."
	case e.Status == http.StatusTooManyRequests:
		return "Rate or concurrency limit reached. Wait a moment, or lower --parallel."
	}
	return ""
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	if c.ForwardIP != "" {
		req.Header.Set("X-Real-IP", c.ForwardIP)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return &Error{Status: resp.StatusCode, Message: fmt.Sprintf("unexpected response (HTTP %d)", resp.StatusCode)}
	}
	if resp.StatusCode >= 400 || !env.Success {
		e := &Error{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		if env.Error != nil {
			e.Code, e.Message = env.Error.Code, env.Error.Message
		}
		return e
	}
	if out != nil {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

// ── Jobs ──────────────────────────────────────────────────────────────────

type Job struct {
	ID          string    `json:"id"`
	Tag         string    `json:"tag"`
	Status      string    `json:"status"` // waiting | processing | finished | error
	CreditsUsed int64     `json:"credits_used"`
	Tasks       []*Task   `json:"tasks"`
	CreatedAt   time.Time `json:"created_at"`
}

type Task struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Operation string         `json:"operation"`
	Status    string         `json:"status"`
	Percent   int            `json:"percent"`
	Message   string         `json:"message"`
	Code      string         `json:"code"`
	UploadURL string         `json:"upload_url"`
	Result    map[string]any `json:"result"`
}

type File struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
}

// Task returns the task with the given name, or nil.
func (j *Job) Task(name string) *Task {
	for _, t := range j.Tasks {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// Files returns the files an export task produced.
func (t *Task) Files() []File {
	if t == nil || t.Result == nil {
		return nil
	}
	raw, err := json.Marshal(t.Result["files"])
	if err != nil {
		return nil
	}
	var files []File
	_ = json.Unmarshal(raw, &files)
	return files
}

// Failure describes why a finished-with-error job failed.
func (j *Job) Failure() string {
	for _, t := range j.Tasks {
		if t.Status == "error" {
			msg := t.Message
			if msg == "" {
				msg = "conversion failed"
			}
			if t.Code != "" {
				return fmt.Sprintf("%s: %s (%s)", t.Name, msg, t.Code)
			}
			return t.Name + ": " + msg
		}
	}
	return "job failed"
}

func (c *Client) CreateJob(ctx context.Context, tag string, tasks map[string]any) (*Job, error) {
	var job Job
	err := c.do(ctx, http.MethodPost, "/v3/jobs?include=tasks", map[string]any{"tag": tag, "tasks": tasks}, &job)
	return &job, err
}

func (c *Client) GetJob(ctx context.Context, id string) (*Job, error) {
	var job Job
	err := c.do(ctx, http.MethodGet, "/v3/jobs/"+url.PathEscape(id)+"?include=tasks", nil, &job)
	return &job, err
}

func (c *Client) CancelJob(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v3/jobs/"+url.PathEscape(id)+"/cancel", nil, nil)
}

func (c *Client) ListJobs(ctx context.Context, status string, perPage int) ([]*Job, error) {
	q := url.Values{}
	if status != "" {
		q.Set("filter[status]", status)
	}
	if perPage > 0 {
		q.Set("per_page", fmt.Sprint(perPage))
	}
	var jobs []*Job
	err := c.do(ctx, http.MethodGet, "/v3/jobs?"+q.Encode(), nil, &jobs)
	return jobs, err
}

// WaitJob polls until the job is finished or failed, calling onUpdate on each poll.
func (c *Client) WaitJob(ctx context.Context, id string, onUpdate func(*Job)) (*Job, error) {
	delay := 700 * time.Millisecond
	for {
		job, err := c.GetJob(ctx, id)
		if err != nil {
			return nil, err
		}
		if onUpdate != nil {
			onUpdate(job)
		}
		if job.Status == "finished" || job.Status == "error" {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 3*time.Second {
			delay += 300 * time.Millisecond
		}
	}
}

// ── Formats ───────────────────────────────────────────────────────────────

type Output struct {
	Format   string `json:"format"`
	Category string `json:"category"`
	Engine   string `json:"engine"`
	Credits  int    `json:"credits"`
}

type InputFormat struct {
	Format   string   `json:"format"`
	Category string   `json:"category"`
	Outputs  []Output `json:"outputs"`
}

type Option struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	Description     string `json:"description"`
	SupportedValues any    `json:"supported_values"`
}

type Engine struct {
	Name    string   `json:"name"`
	Credits int      `json:"credits"`
	Options []Option `json:"options"`
}

// public is a copy without the key: format lookups are public, and a bad key
// must not stop them working.
func (c *Client) public() *Client {
	cp := *c
	cp.Key = ""
	return &cp
}

// Outputs lists what an input format converts to (no API key needed).
func (c *Client) Outputs(ctx context.Context, input string) (*InputFormat, error) {
	var f InputFormat
	err := c.public().do(ctx, http.MethodGet, "/v3/operations?input="+url.QueryEscape(input), nil, &f)
	return &f, err
}

// ── Account ───────────────────────────────────────────────────────────────

// Me is GET /v3/me: who the key belongs to, the team and its credits.
type Me struct {
	User struct {
		Email    string `json:"email"`
		FullName string `json:"full_name"`
	} `json:"user"`
	Team struct {
		Name string `json:"name"`
	} `json:"team"`
	APIKey *struct {
		Name string `json:"name"`
	} `json:"api_key"`
	Credits *struct {
		Mode           string `json:"mode"` // daily | wallet
		DailyLimit     int64  `json:"daily_limit"`
		DailyRemaining int64  `json:"daily_remaining"`
		TotalRemaining int64  `json:"total_remaining"`
	} `json:"credits"`
}

func (c *Client) Me(ctx context.Context) (*Me, error) {
	var m Me
	err := c.do(ctx, http.MethodGet, "/v3/me", nil, &m)
	return &m, err
}

// CreditsText describes the credits left: "1,240 credits" or "12 of 20 today".
func (m *Me) CreditsText() string {
	if m.Credits == nil {
		return ""
	}
	if m.Credits.Mode == "daily" {
		return fmt.Sprintf("%s of %s free credits left today", comma(m.Credits.DailyRemaining), comma(m.Credits.DailyLimit))
	}
	return comma(m.Credits.TotalRemaining) + " credits"
}

func comma(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// ── Device sign-in (OAuth device flow, runconvert login) ──────────────────

// ClientID is the CLI's OAuth client, known to the API without registration.
const ClientID = "runconvert-cli"

type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// OAuthError is an OAuth error answer, e.g. authorization_pending.
type OAuthError struct{ Code, Description string }

func (e *OAuthError) Error() string {
	if e.Description != "" {
		return e.Description
	}
	return e.Code
}

// form POSTs a form to an OAuth endpoint, which answers in OAuth's own JSON
// (not the API's envelope).
func (c *Client) form(ctx context.Context, path string, vals url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, strings.NewReader(vals.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error       any    `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(raw, &e)
		if code, ok := e.Error.(string); ok && code != "" {
			return &OAuthError{Code: code, Description: e.Description}
		}
		if m, ok := e.Error.(map[string]any); ok { // the API's envelope
			msg, _ := m["message"].(string)
			code, _ := m["code"].(string)
			return &Error{Status: resp.StatusCode, Code: code, Message: msg}
		}
		return &Error{Status: resp.StatusCode, Message: fmt.Sprintf("sign-in failed (HTTP %d)", resp.StatusCode)}
	}
	return json.Unmarshal(raw, out)
}

// StartDevice asks for a code to show (POST /v3/oauth/device).
func (c *Client) StartDevice(ctx context.Context, deviceName string) (*DeviceCode, error) {
	var d DeviceCode
	err := c.form(ctx, "/v3/oauth/device", url.Values{"client_id": {ClientID}, "scope": {"convert"}, "device_name": {deviceName}}, &d)
	return &d, err
}

// DeviceToken checks once whether the code was approved; an *OAuthError says
// authorization_pending, slow_down, access_denied or expired_token.
func (c *Client) DeviceToken(ctx context.Context, deviceCode string) (string, error) {
	var t struct {
		AccessToken string `json:"access_token"`
	}
	err := c.form(ctx, "/v3/oauth/token", url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode}, "client_id": {ClientID},
	}, &t)
	return t.AccessToken, err
}

// Catalog lists the categories and every input format, or only one
// category's formats (no API key needed).
func (c *Client) Catalog(ctx context.Context, category string) ([]string, []InputFormat, error) {
	var out struct {
		Categories []string      `json:"categories"`
		Formats    []InputFormat `json:"formats"`
	}
	path := "/v3/operations"
	if category != "" {
		path += "?category=" + url.QueryEscape(category)
	}
	err := c.public().do(ctx, http.MethodGet, path, nil, &out)
	return out.Categories, out.Formats, err
}

// Engines lists the engines, credits and options for one conversion pair.
func (c *Client) Engines(ctx context.Context, input, output string) ([]Engine, error) {
	var out struct {
		Engines []Engine `json:"engines"`
	}
	err := c.public().do(ctx, http.MethodGet, "/v3/operations?input="+url.QueryEscape(input)+"&output="+url.QueryEscape(output), nil, &out)
	return out.Engines, err
}

// ── Transfers ─────────────────────────────────────────────────────────────

type progressReader struct {
	r     io.Reader
	n     int64
	total int64
	fn    func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.fn != nil {
		p.fn(p.n, p.total)
	}
	return n, err
}

// Upload PUTs a local file to an import/direct/upload task's upload URL.
func (c *Client) Upload(ctx context.Context, uploadURL, path string, onProgress func(done, total int64)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	u, err := url.Parse(uploadURL)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("filename", filepath.Base(path))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), &progressReader{r: f, total: st.Size(), fn: onProgress})
	if err != nil {
		return err
	}
	req.ContentLength = st.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.Transfer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("upload failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// Download saves url to dest, writing to a temporary file first so a failed
// download never leaves a half-written output behind.
func (c *Client) Download(ctx context.Context, fileURL, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.Transfer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("download failed (HTTP %d)", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".runconvert-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// IsAPIError reports whether err is an API error, returning it.
func IsAPIError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}
