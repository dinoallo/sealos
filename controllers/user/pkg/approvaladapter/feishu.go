/*
Copyright 2026 labring.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package approvaladapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const DefaultFeishuBaseURL = "https://open.feishu.cn"

// FeishuClientConfig contains Feishu application credentials and the API
// transport. The app secret is kept in memory and is never included in an
// error or response.
type FeishuClientConfig struct {
	BaseURL    string
	AppID      string
	AppSecret  string
	UserIDType string
	HTTPClient *http.Client
	Clock      func() time.Time
}

// FeishuClient is an API client for the minimum approval and user endpoints
// required by the adapter.
type FeishuClient struct {
	baseURL    string
	appID      string
	appSecret  string
	userIDType string
	httpClient *http.Client
	clock      func() time.Time

	mu        sync.Mutex
	token     string
	tokenTill time.Time
}

type feishuAPIError struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type tenantTokenResponse struct {
	feishuAPIError
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int64  `json:"expire"`
}

type instanceResponse struct {
	feishuAPIError
	Data struct {
		ApprovalCode string `json:"approval_code"`
		InstanceCode string `json:"instance_code"`
		Status       string `json:"status"`
		UserID       string `json:"user_id"`
		OpenID       string `json:"open_id"`
		Form         string `json:"form"`
	} `json:"data"`
}

type userResponse struct {
	feishuAPIError
	Data struct {
		User struct {
			Email string `json:"email"`
		} `json:"user"`
	} `json:"data"`
}

func NewFeishuClient(config FeishuClientConfig) (*FeishuClient, error) {
	if config.BaseURL == "" {
		config.BaseURL = DefaultFeishuBaseURL
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Feishu base URL must be an HTTPS URL without query or fragment")
	}
	if config.AppID == "" || config.AppSecret == "" {
		return nil, errors.New("Feishu app ID and app secret are required")
	}
	if config.UserIDType == "" {
		config.UserIDType = "open_id"
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &FeishuClient{baseURL: strings.TrimRight(config.BaseURL, "/"), appID: config.AppID, appSecret: config.AppSecret, userIDType: config.UserIDType, httpClient: config.HTTPClient, clock: config.Clock}, nil
}

func (c *FeishuClient) GetInstance(ctx context.Context, instanceCode string) (ApprovalInstance, error) {
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return ApprovalInstance{}, err
	}
	query := url.Values{"user_id_type": []string{c.userIDType}}
	var response instanceResponse
	if err := c.get(ctx, path.Join("/open-apis/approval/v4/instances", url.PathEscape(instanceCode)), query, token, &response); err != nil {
		return ApprovalInstance{}, err
	}
	if response.Code != 0 {
		return ApprovalInstance{}, fmt.Errorf("Feishu approval API returned code %d", response.Code)
	}
	return ApprovalInstance{
		InstanceCode: response.Data.InstanceCode,
		ApprovalCode: response.Data.ApprovalCode,
		Status:       response.Data.Status,
		InitiatorID:  response.Data.UserID,
		Form:         response.Data.Form,
	}, nil
}

func (c *FeishuClient) GetUserEmail(ctx context.Context, userID string) (string, error) {
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return "", err
	}
	query := url.Values{"user_id_type": []string{c.userIDType}}
	var response userResponse
	if err := c.get(ctx, path.Join("/open-apis/contact/v3/users", url.PathEscape(userID)), query, token, &response); err != nil {
		return "", err
	}
	if response.Code != 0 {
		return "", fmt.Errorf("Feishu user API returned code %d", response.Code)
	}
	return response.Data.User.Email, nil
}

func (c *FeishuClient) tenantAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && c.clock().Before(c.tokenTill) {
		token := c.token
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	body, err := json.Marshal(map[string]string{"app_id": c.appID, "app_secret": c.appSecret})
	if err != nil {
		return "", errors.New("encode Feishu token request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", errors.New("create Feishu token request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("request Feishu tenant token: %w", err)
	}
	defer response.Body.Close()
	var result tenantTokenResponse
	if err := decodeResponse(response, &result); err != nil {
		return "", fmt.Errorf("decode Feishu tenant token: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Code != 0 || result.TenantAccessToken == "" {
		return "", fmt.Errorf("Feishu tenant token request failed with HTTP %d", response.StatusCode)
	}
	lifetime := time.Duration(result.Expire) * time.Second
	if lifetime <= time.Minute {
		return "", errors.New("Feishu tenant token lifetime is too short")
	}
	c.mu.Lock()
	c.token = result.TenantAccessToken
	c.tokenTill = c.clock().Add(lifetime - time.Minute)
	c.mu.Unlock()
	return result.TenantAccessToken, nil
}

func (c *FeishuClient) get(ctx context.Context, endpoint string, query url.Values, token string, result interface{}) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return errors.New("create Feishu API request")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request Feishu API: %w", err)
	}
	defer response.Body.Close()
	if err := decodeResponse(response, result); err != nil {
		return fmt.Errorf("decode Feishu API response: %w", err)
	}
	return nil
}

func decodeResponse(response *http.Response, result interface{}) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, result); err != nil {
		return err
	}
	return nil
}
