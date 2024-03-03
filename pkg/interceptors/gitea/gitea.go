/*
Copyright 2024 The Tekton Authors

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

package gitea

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"code.gitea.io/sdk/gitea"
	triggersv1 "github.com/tektoncd/triggers/pkg/apis/triggers/v1beta1"
	"github.com/tektoncd/triggers/pkg/interceptors"
	"google.golang.org/grpc/codes"
)

var _ triggersv1.InterceptorInterface = (*InterceptorImpl)(nil)

var acceptedEventTypes = []string{"pull_request", "push"}

type testURLKey string

const (
	changedFilesExtensionsKey            = "changed_files"
	testURL                   testURLKey = "TESTURL"
)

// ErrInvalidContentType is returned when the content-type is not a JSON body.
var ErrInvalidContentType = errors.New("form parameter encoding not supported, please change the hook to send JSON payloads")

type InterceptorImpl struct {
	SecretGetter interceptors.SecretGetter
}

type payloadDetails struct {
	PrNumber     int
	Owner        string
	Repository   string
	ChangedFiles string
}
func NewInterceptor(sg interceptors.SecretGetter) *InterceptorImpl {
	return &InterceptorImpl{
		SecretGetter: sg,
	}
}

// InterceptorParams provides a webhook to intercept and pre-process events
type InterceptorParams struct {
	SecretRef *triggersv1.SecretRef `json:"secretRef,omitempty"`
	EventTypes      []string        `json:"eventTypes,omitempty"`
	AddChangedFiles AddChangedFiles `json:"addChangedFiles,omitempty"`
	BaseURL         string          `json:"baseUrl,omitempty"`
}

type AddChangedFiles struct {
	Enabled             bool                  `json:"enabled,omitempty"`
	PersonalAccessToken *triggersv1.SecretRef `json:"personalAccessToken,omitempty"`
}

func (w *InterceptorImpl) Process(ctx context.Context, r *triggersv1.InterceptorRequest) *triggersv1.InterceptorResponse {
	headers := interceptors.Canonical(r.Header)
	if v := headers.Get("Content-Type"); v == "application/x-www-form-urlencoded" {
		return interceptors.Fail(codes.InvalidArgument, ErrInvalidContentType.Error())
	}

	p := InterceptorParams{}
	if err := interceptors.UnmarshalParams(r.InterceptorParams, &p); err != nil {
		return interceptors.Failf(codes.InvalidArgument, "failed to parse interceptor params: %v", err)
	}

	actualEvent := headers.Get("X-Gitea-Event")

	// Check if the event type is in the allow-list
	if p.EventTypes != nil {
		isAllowed := false
		for _, allowedEvent := range p.EventTypes {
			if actualEvent == allowedEvent {
				isAllowed = true
				break
			}
		}
		if !isAllowed {
			return interceptors.Failf(codes.FailedPrecondition, "event type %s is not allowed", actualEvent)
		}
	}

	if p.AddChangedFiles.Enabled {
		shouldAddChangedFiles := false
		for _, allowedEvent := range acceptedEventTypes {
			if actualEvent == allowedEvent {
				shouldAddChangedFiles = true
				break
			}
		}
		if !shouldAddChangedFiles {
			return &triggersv1.InterceptorResponse{
				Continue: true,
			}
		}

		if r.Context == nil {
			return interceptors.Failf(codes.InvalidArgument, "no request context passed")
		}

		secretToken, err := w.getGiteaTokenSecret(ctx, r, p)
		if err != nil {
			return interceptors.Failf(codes.FailedPrecondition, "error getting secret: %v", err)
		}

		payload, err := parseBodyForChangedFiles(r.Body, actualEvent)
		if err != nil {
			return interceptors.Failf(codes.FailedPrecondition, "error parsing body: %v", err)
		}

		var changedFiles string
		if actualEvent == "pull_request" {
			changedFiles, err = getChangedFilesFromPr(ctx, payload, p.BaseURL, secretToken)
			if err != nil {
				return interceptors.Failf(codes.FailedPrecondition, "error getting changed files: %v", err)
			}
		} else {
			changedFiles = payload.ChangedFiles
		}

		return &triggersv1.InterceptorResponse{
			Extensions: map[string]interface{}{
				changedFilesExtensionsKey: changedFiles,
			},
			Continue: true,
		}

	}

	return &triggersv1.InterceptorResponse{
		Continue: true,
	}
}

func (w *InterceptorImpl) getGiteaTokenSecret(ctx context.Context, r *triggersv1.InterceptorRequest, p InterceptorParams) (string, error) {
	if p.AddChangedFiles.PersonalAccessToken == nil {
		return "", nil
	}
	if p.AddChangedFiles.PersonalAccessToken.SecretKey == "" {
		return "", fmt.Errorf("gitea interceptor giteaToken.secretKey is empty")
	}
	ns, _ := triggersv1.ParseTriggerID(r.Context.TriggerID)
	secretToken, err := w.SecretGetter.Get(ctx, ns, p.AddChangedFiles.PersonalAccessToken)
	if err != nil {
		return "", err
	}
	return string(secretToken), nil
}

func parseBodyForChangedFiles(body string, eventType string) (payloadDetails, error) {
	results := payloadDetails{}
	if body == "" {
		return results, fmt.Errorf("body is empty")
	}

	var jsonMap map[string]interface{}
	err := json.Unmarshal([]byte(body), &jsonMap)
	if err != nil {
		return results, err
	}

	var prNum int
	_, ok := jsonMap["number"]
	if ok {
		prNum = int(jsonMap["number"].(float64))
	} else {
		if eventType == "pull_request" {
			return results, fmt.Errorf("pull_request body missing 'number' field")
		}
		prNum = -1
	}

	repoSection, ok := jsonMap["repository"].(map[string]interface{})
	if !ok {
		return results, fmt.Errorf("payload body missing 'repository' field")
	}

	fullName, ok := repoSection["full_name"].(string)
	if !ok {
		return results, fmt.Errorf("payload body missing 'repository.full_name' field")
	}

	changedFiles := []string{}

	commitsSection, ok := jsonMap["commits"].([]interface{})
	if ok {

		for _, commit := range commitsSection {
			addedFiles, ok := commit.(map[string]interface{})["added"].([]interface{})
			if !ok {
				return results, fmt.Errorf("payload body missing 'commits.*.added' field")
			}

			modifiedFiles, ok := commit.(map[string]interface{})["modified"].([]interface{})
			if !ok {
				return results, fmt.Errorf("payload body missing 'commits.*.modified' field")
			}

			removedFiles, ok := commit.(map[string]interface{})["removed"].([]interface{})
			if !ok {
				return results, fmt.Errorf("payload body missing 'commits.*.removed' field")
			}
			for _, fileName := range addedFiles {
				changedFiles = append(changedFiles, fmt.Sprintf("%v", fileName))
			}

			for _, fileName := range modifiedFiles {
				changedFiles = append(changedFiles, fmt.Sprintf("%v", fileName))
			}

			for _, fileName := range removedFiles {
				changedFiles = append(changedFiles, fmt.Sprintf("%v", fileName))
			}
		}
	}

	results = payloadDetails{
		PrNumber:     prNum,
		Owner:        strings.Split(fullName, "/")[0],
		Repository:   strings.Split(fullName, "/")[1],
		ChangedFiles: strings.Join(changedFiles, ","),
	}
	return results, nil
}

func getChangedFilesFromPr(ctx context.Context, payload payloadDetails, baseURL string, token string) (string, error) {
	page := 1
	changedFiles := []string{}
	caCertFile := ""
	username := ""
	insecure := false

	client, err := makeClient(ctx, baseURL, token, username, caCertFile, insecure)
	if err != nil {
		return "", err
	}

	for {
		opt := gitea.ListPullRequestFilesOptions{
			ListOptions: gitea.ListOptions{
				Page:     page,
				PageSize: 10,
			},
		}
		files, res, err := client.ListPullRequestFiles(payload.Owner, payload.Repository, int64(payload.PrNumber), opt)
		if err != nil {
			return "", err
		}
		for _, file := range files {
			changedFiles = append(changedFiles, file.Filename)
		}
		hasmore := res.Header.Get("x-hasmore")
		fmt.Printf("i have more to get %q\n", hasmore)
		if hasmore != "true" {
			break
		}
		page += 1
	}

	return strings.Join(changedFiles, ","), nil
}

func makeClient(ctx context.Context, baseURL, token, username, caCertFile string, insecure bool) (*gitea.Client, error) {
	var tlsConfig tls.Config
	// If a CACertFile has been specified, use that for cert validation
	if caCertFile != "" {
		caCert, err := os.ReadFile(caCertFile)
		if err != nil {
			return nil, err
		}

		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}

	// If configured as insecure, turn off SSL verification
	tlsConfig.InsecureSkipVerify = insecure

	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tlsConfig
	t.MaxIdleConnsPerHost = 100
	t.TLSHandshakeTimeout = 10 * time.Second

	if baseURL == "" {
		baseURL = "https://gitea.com"
	}

	// overwrite url if testURL is passed within the ctx
	if ctx.Value(testURL) != nil {
		baseURL = fmt.Sprintf("%v", ctx.Value(testURL))
	}

	// var httpClient *http.Client
	var client *gitea.Client
	var err error
	if token != "" {
		client, err = gitea.NewClient(baseURL, gitea.SetToken(token)) //, gitea.SetHTTPClient(httpClient))
		if err != nil {
			return nil, err
		}
	}
	return client, nil
}

