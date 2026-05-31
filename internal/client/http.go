package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxResponseBytes = 256 << 20 // 256 MB — prevents OOM from malicious nodes

type HTTP struct {
	base   string
	client *http.Client
}

func NewHTTP(base string) *HTTP {
	return &HTTP{
		base: strings.TrimRight(base, "/"),
		client: &http.Client{
			Timeout: 2 * time.Minute,
		},
	}
}

func (h *HTTP) Get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, h.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func (h *HTTP) GetBytes(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, h.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := limitedReadBody(resp)
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return limitedReadBody(resp)
}

func (h *HTTP) Post(path string, in any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, h.base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func decodeResponse(resp *http.Response, out any) error {
	body, err := limitedReadBody(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// limitedReadBody reads up to maxResponseBytes from the response body.
// Error responses are capped at 4 KB to keep error messages readable.
func limitedReadBody(resp *http.Response) ([]byte, error) {
	limit := int64(maxResponseBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limit = 4096
	}
	n, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if int64(len(n)) > limit {
		return nil, fmt.Errorf("http response exceeds %d bytes", limit)
	}
	return n, err
}
