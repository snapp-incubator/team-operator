package iam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type team struct {
	Name   string   `json:"name"`
	Admins []string `json:"admins"`
}

// FetchTeamAdmins returns the list of admin usernames for the given team from the IAM API.
// baseURL is the base URL of the IAM team API (e.g. http://host/api/teams).
func FetchTeamAdmins(ctx context.Context, httpClient *http.Client, baseURL, teamName string) ([]string, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	teamURL := strings.TrimRight(baseURL, "/") + "/" + url.PathEscape(teamName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, teamURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("IAM team API returned status %d for team %s", resp.StatusCode, teamName)
	}

	var t team
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, err
	}

	return t.Admins, nil
}
