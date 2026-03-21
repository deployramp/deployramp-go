package deployramp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// apiClient handles HTTP communication with the DeployRamp API.
type apiClient struct {
	baseURL     string
	publicToken string
	httpClient  *http.Client
}

func newAPIClient(baseURL, publicToken string) *apiClient {
	return &apiClient{
		baseURL:     baseURL,
		publicToken: publicToken,
		httpClient:  &http.Client{},
	}
}

// fetchFlags retrieves all flags for the given user and traits.
func (c *apiClient) fetchFlags(userID string, traits map[string]string) (*fetchFlagsResponse, error) {
	reqBody := fetchFlagsRequest{
		UserID: userID,
		Traits: traits,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/sdk/flags", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.publicToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch flags: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch flags: status %d", resp.StatusCode)
	}

	var result fetchFlagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// reportError sends an error report to the API. This is fire-and-forget.
func (c *apiClient) reportError(flagName, message, stack, userID string, traits map[string]string) {
	go func() {
		reqBody := reportRequest{
			FlagName: flagName,
			Message:  message,
			Stack:    stack,
			UserID:   userID,
			Traits:   traits,
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			return
		}

		req, err := http.NewRequest("POST", c.baseURL+"/api/sdk/report", bytes.NewReader(body))
		if err != nil {
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.publicToken)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return
		}
		resp.Body.Close()
	}()
}
