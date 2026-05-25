package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// apiBase returns the base URL from env or default.
func apiBase() string {
	if u := os.Getenv("RAZIEL_API_URL"); u != "" {
		return u
	}
	return "http://localhost:8000"
}

// apiKey returns the API key from env.
func apiKey() string {
	return os.Getenv("RAZIEL_API_SECRET")
}

type apiClient struct {
	base   string
	secret string
	http   *http.Client
}

func newClient() *apiClient {
	return &apiClient{
		base:   apiBase(),
		secret: apiKey(),
		http:   &http.Client{},
	}
}

func (c *apiClient) get(path string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeResponse(resp)
}

func (c *apiClient) delete(path string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodDelete, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeResponse(resp)
}

func decodeResponse(resp *http.Response) (map[string]any, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w\nbody: %s", err, body)
	}
	return result, nil
}
