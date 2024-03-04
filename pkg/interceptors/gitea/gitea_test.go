/*
Copyright 2019 The Tekton Authors

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
	"net/http"
	"net/http/httptest"
	"testing"

	"code.gitea.io/sdk/gitea"
	triggersv1 "github.com/tektoncd/triggers/pkg/apis/triggers/v1beta1"
	"github.com/tektoncd/triggers/pkg/interceptors"
	"github.com/tektoncd/triggers/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakekubeclient "knative.dev/pkg/client/injection/kube/client/fake"
)

func TestInterceptor_ExecuteTrigger_with_invalid_content_type(t *testing.T) {
	ctx, _ := test.SetupFakeContext(t)
	req := &triggersv1.InterceptorRequest{
		Body: `{}`,
		Header: http.Header{
			"Content-Type":    []string{"application/x-www-form-urlencoded"},
			"X-Hub-Signature": []string{"foo"},
		},
		InterceptorParams: map[string]interface{}{},
		Context: &triggersv1.TriggerContext{
			EventURL:  "https://testing.example.com",
			EventID:   "abcde",
			TriggerID: "namespaces/default/triggers/example-trigger",
		},
	}
	w := &InterceptorImpl{
		SecretGetter: interceptors.DefaultSecretGetter(fakekubeclient.Get(ctx).CoreV1()),
	}
	res := w.Process(ctx, req)
	if res.Continue {
		t.Fatalf("Interceptor.Process() expected res.Continue to be : %t but got %t.\n Status.Err(): %v", false, res.Continue, res.Status.Err())
	}
	if res.Status.Message != ErrInvalidContentType.Error() {
		t.Fatalf("got error %v, want %v", res.Status.Err(), ErrInvalidContentType)
	}
}

func TestInterceptor_Process_InvalidParams(t *testing.T) {
	ctx, _ := test.SetupFakeContext(t)

	w := &InterceptorImpl{
		SecretGetter: interceptors.DefaultSecretGetter(fakekubeclient.Get(ctx).CoreV1()),
	}

	req := &triggersv1.InterceptorRequest{
		Body: `{}`,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		InterceptorParams: map[string]interface{}{
			"blah": func() {},
		},
		Context: &triggersv1.TriggerContext{
			EventURL:  "https://testing.example.com",
			EventID:   "abcde",
			TriggerID: "namespaces/default/triggers/example-trigger",
		},
	}

	res := w.Process(ctx, req)
	if res.Continue {
		t.Fatalf("Interceptor.Process() expected res.Continue to be false but got %t. \nStatus.Err(): %v", res.Continue, res.Status.Err())
	}
}

func TestInterceptor_ExecuteTrigger_Changed_Files_Pull_Request(t *testing.T) {
	var secretToken = "secret"
	tests := []struct {
		name               string
		giteaServerReply   string
		secret             *corev1.Secret
		interceptorRequest *triggersv1.InterceptorRequest
		wantResContinue    bool
		want               string
		wantStatusMessage  string
	}{
		{
			name:             "changed_files",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/prod/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/qa/main.tf","status":"changed","additions":1,"deletions":1,"changes":2}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"number":130,"repository":{"full_name":"devOops/IaC"}}`,
				Header: map[string][]string{"X-Gitea-Event": {"pull_request"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"baseURL":    "https://gitea.v3nc.org",
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   true,
			want:              "terraform/envs/dev/main.tf,terraform/envs/prod/main.tf,terraform/envs/qa/main.tf",
			wantStatusMessage: "",
		},
		{
			name:             "empty body, failure",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/prod/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/qa/main.tf","status":"changed","additions":1,"deletions":1,"changes":2}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   "",
				Header: map[string][]string{"X-Gitea-Event": {"pull_request"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   false,
			want:              "",
			wantStatusMessage: "error parsing body: body is empty",
		},
		{
			name:             "non json body, failure",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/prod/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/qa/main.tf","status":"changed","additions":1,"deletions":1,"changes":2}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `this is not json`,
				Header: map[string][]string{"X-Gitea-Event": {"pull_request"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   false,
			want:              "",
			wantStatusMessage: "error parsing body: invalid character 'h' in literal true (expecting 'r')",
		},
		{
			name:             "pull request, missing 'number' json field",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/prod/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/qa/main.tf","status":"changed","additions":1,"deletions":1,"changes":2}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"repository":{"full_name":"devOops/IaC"}}`,
				Header: map[string][]string{"X-Gitea-Event": {"pull_request"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   false,
			want:              "",
			wantStatusMessage: "error parsing body: pull_request body missing 'number' field",
		},
		{
			name:             "missing repository json field, failure",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/prod/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/qa/main.tf","status":"changed","additions":1,"deletions":1,"changes":2}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"number":130}`,
				Header: map[string][]string{"X-Gitea-Event": {"pull_request"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   false,
			want:              "",
			wantStatusMessage: "error parsing body: payload body missing 'repository' field",
		},
		{
			name:             "missing full_name json field, failure",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/prod/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/qa/main.tf","status":"changed","additions":1,"deletions":1,"changes":2}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"number":130,"repository":{}}`,
				Header: map[string][]string{"X-Gitea-Event": {"pull_request"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   false,
			want:              "",
			wantStatusMessage: "error parsing body: payload body missing 'repository.full_name' field",
		},
		{
			name:             "event type not push or pull_request, failure",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/prod/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/qa/main.tf","status":"changed","additions":1,"deletions":1,"changes":2}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"number":130,"repository":{"full_name":"devOops/IaC"}}`,
				Header: map[string][]string{"X-Gitea-Event": {"nothing"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push", "nothing"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   true,
			want:              "",
			wantStatusMessage: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/api/v1/version" {
					writer.Write([]byte(`{"version": "1.21.7"}`))
				} else {
					writer.Write([]byte(tt.giteaServerReply))
				}
			}))
			ctx, _ := test.SetupFakeContext(t)

			ctx = context.WithValue(ctx, testURL, ts.URL)
			clientset := fakekubeclient.Get(ctx)
			if tt.secret != nil {
				tt.secret.Namespace = metav1.NamespaceDefault
				ctx, clientset = fakekubeclient.With(ctx, tt.secret)
			}

			w := &InterceptorImpl{
				SecretGetter: interceptors.DefaultSecretGetter(clientset.CoreV1()),
			}
			res := w.Process(ctx, tt.interceptorRequest)

			if res.Continue != tt.wantResContinue {
				t.Fatalf("Interceptor.Process() expected res.Continue to be %t but got %t. \nStatus.Err(): %v", tt.wantResContinue, res.Continue, res.Status.Err())
			}

			if res.Status.Message != tt.wantStatusMessage {
				t.Fatalf("Interceptor.Process() expected res.Status.Message to be '%s' but got '%s'", tt.wantStatusMessage, res.Status.Message)
			}

			changedFilesExt := res.Extensions[changedFilesExtensionsKey]
			if changedFilesExt == nil {
				changedFilesExt = ""
			}
			if tt.want != changedFilesExt {
				t.Fatalf("Interceptor.Process() got %v '%v', want '%v'", changedFilesExtensionsKey, changedFilesExt, tt.want)
			}
		})
	}
}

func TestInterceptor_ExecuteTrigger_Changed_Files_Push(t *testing.T) {
	var secretToken = "secret"
	tests := []struct {
		name               string
		giteaServerReply   string
		secret             *corev1.Secret
		interceptorRequest *triggersv1.InterceptorRequest
		wantResContinue    bool
		want               string
		wantStatusMessage  string
	}{
		{
			name:             "changed_files",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf"},{"filename":"terraform/envs/prod/main.tf"},{"filename":"terraform/envs/qa/main.tf"}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"repository":{"full_name":"testowner/testrepo","clone_url":"https://gitea.com/testowner/testrepo.git"},"commits":[{"added":["api/v1beta1/tektonhelperconfig_types.go","config/crd/bases/tekton-helper..com_tektonhelperconfigs.yaml"],"removed":["config/samples/tektonhelperconfig-oomkillpipeline.yaml","config/samples/tektonhelperconfig-timeout.yaml"],"modified":["controllers/tektonhelperconfig_controller.go"]}]}`,
				Header: map[string][]string{"X-Hub-Signature": {"foo"}, "X-Gitea-Event": {"push"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   true,
			want:              "api/v1beta1/tektonhelperconfig_types.go,config/crd/bases/tekton-helper..com_tektonhelperconfigs.yaml,controllers/tektonhelperconfig_controller.go,config/samples/tektonhelperconfig-oomkillpipeline.yaml,config/samples/tektonhelperconfig-timeout.yaml",
			wantStatusMessage: "",
		},
		{
			name:             "missing commits added json field, failure",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/prod/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/qa/main.tf","status":"changed","additions":1,"deletions":1,"changes":2}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"repository":{"full_name":"testowner/testrepo","clone_url":"https://gitea.com/testowner/testrepo.git"},"commits":[{"removed":["config/samples/tektonhelperconfig-oomkillpipeline.yaml","config/samples/tektonhelperconfig-timeout.yaml"],"modified":["controllers/tektonhelperconfig_controller.go"]}]}`,
				Header: map[string][]string{"X-Hub-Signature": {"foo"}, "X-Gitea-Event": {"push"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   false,
			want:              "",
			wantStatusMessage: "error parsing body: payload body missing 'commits.*.added' field",
		},
		{
			name:             "missing commits removed json field, failure",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/prod/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/qa/main.tf","status":"changed","additions":1,"deletions":1,"changes":2}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"repository":{"full_name":"testowner/testrepo","clone_url":"https://gitea.com/testowner/testrepo.git"},"commits":[{"added":["api/v1beta1/tektonhelperconfig_types.go","config/crd/bases/tekton-helper..com_tektonhelperconfigs.yaml"],"modified":["controllers/tektonhelperconfig_controller.go"]}]}`,
				Header: map[string][]string{"X-Hub-Signature": {"foo"}, "X-Gitea-Event": {"push"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   false,
			want:              "",
			wantStatusMessage: "error parsing body: payload body missing 'commits.*.removed' field",
		},
		{
			name:             "missing commits modified json field, failure",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/prod/main.tf","status":"changed","additions":1,"deletions":1,"changes":2},{"filename":"terraform/envs/qa/main.tf","status":"changed","additions":1,"deletions":1,"changes":2}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"repository":{"full_name":"testowner/testrepo","clone_url":"https://gitea.com/testowner/testrepo.git"},"commits":[{"added":["api/v1beta1/tektonhelperconfig_types.go","config/crd/bases/tekton-helper..com_tektonhelperconfigs.yaml"],"removed":["config/samples/tektonhelperconfig-oomkillpipeline.yaml","config/samples/tektonhelperconfig-timeout.yaml"]}]}`,
				Header: map[string][]string{"X-Hub-Signature": {"foo"}, "X-Gitea-Event": {"push"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   false,
			want:              "",
			wantStatusMessage: "error parsing body: payload body missing 'commits.*.modified' field",
		},
		{
			name:             "no context with secretRef",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf"},{"filename":"terraform/envs/prod/main.tf"},{"filename":"terraform/envs/qa/main.tf"}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"repository":{"full_name":"testowner/testrepo","clone_url":"https://gitea.com/testowner/testrepo.git"},"commits":[{"added":["api/v1beta1/tektonhelperconfig_types.go","config/crd/bases/tekton-helper..com_tektonhelperconfigs.yaml"],"removed":["config/samples/tektonhelperconfig-oomkillpipeline.yaml","config/samples/tektonhelperconfig-timeout.yaml"]}]}`,
				Header: map[string][]string{"X-Hub-Signature": {"foo"}, "X-Gitea-Event": {"push"}},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"secretRef": &triggersv1.SecretRef{
						SecretName: "mysecret",
						SecretKey:  "token",
					},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   false,
			want:              "",
			wantStatusMessage: "no request context passed",
		},
		{
			name:             "no context",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf"},{"filename":"terraform/envs/prod/main.tf"},{"filename":"terraform/envs/qa/main.tf"}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"repository":{"full_name":"testowner/testrepo","clone_url":"https://gitea.com/testowner/testrepo.git"},"commits":[{"added":["api/v1beta1/tektonhelperconfig_types.go","config/crd/bases/tekton-helper..com_tektonhelperconfigs.yaml"],"removed":["config/samples/tektonhelperconfig-oomkillpipeline.yaml","config/samples/tektonhelperconfig-timeout.yaml"]}]}`,
				Header: map[string][]string{"X-Hub-Signature": {"foo"}, "X-Gitea-Event": {"push"}},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   false,
			want:              "",
			wantStatusMessage: "no request context passed",
		},
		{
			name:             "invalid secret",
			giteaServerReply: `[{"filename":"terraform/envs/dev/main.tf"},{"filename":"terraform/envs/prod/main.tf"},{"filename":"terraform/envs/qa/main.tf"}]`,
			interceptorRequest: &triggersv1.InterceptorRequest{
				Body:   `{"repository":{"full_name":"testowner/testrepo","clone_url":"https://gitea.com/testowner/testrepo.git"},"commits":[{"added":["api/v1beta1/tektonhelperconfig_types.go","config/crd/bases/tekton-helper..com_tektonhelperconfigs.yaml"],"removed":["config/samples/tektonhelperconfig-oomkillpipeline.yaml","config/samples/tektonhelperconfig-timeout.yaml"],"modified":["controllers/tektonhelperconfig_controller.go"]}]}`,
				Header: map[string][]string{"X-Hub-Signature": {"foo"}, "X-Gitea-Event": {"push"}},
				Context: &triggersv1.TriggerContext{
					EventURL:  "https://testing.example.com",
					EventID:   "abcde",
					TriggerID: "namespaces/default/triggers/example-trigger",
				},
				InterceptorParams: map[string]interface{}{
					"eventTypes": []string{"pull_request", "push"},
					"addChangedFiles": &AddChangedFiles{
						Enabled: true,
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "missingsecret",
							SecretKey:  "token",
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			wantResContinue:   false,
			want:              "",
			wantStatusMessage: "error getting secret: secrets \"missingsecret\" not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/api/v1/version" {
					writer.Write([]byte(`{"version": "1.21.7"}`))
				} else {
					writer.Write([]byte(tt.giteaServerReply))
				}
			}))
			ctx, _ := test.SetupFakeContext(t)

			ctx = context.WithValue(ctx, testURL, ts.URL)
			clientset := fakekubeclient.Get(ctx)
			if tt.secret != nil {
				tt.secret.Namespace = metav1.NamespaceDefault
				ctx, clientset = fakekubeclient.With(ctx, tt.secret)
			}

			w := &InterceptorImpl{
				SecretGetter: interceptors.DefaultSecretGetter(clientset.CoreV1()),
			}
			res := w.Process(ctx, tt.interceptorRequest)

			if res.Continue != tt.wantResContinue {
				t.Fatalf("Interceptor.Process() expected res.Continue to be %t but got %t. \nStatus.Err(): %v", tt.wantResContinue, res.Continue, res.Status.Err())
			}

			if res.Status.Message != tt.wantStatusMessage {
				t.Fatalf("Interceptor.Process() expected res.Status.Message to be '%s' but got '%s'", tt.wantStatusMessage, res.Status.Message)
			}

			changedFilesExt := res.Extensions[changedFilesExtensionsKey]
			if changedFilesExt == nil {
				changedFilesExt = ""
			}
			if tt.want != changedFilesExt {
				t.Fatalf("Interceptor.Process() got %v '%v', want '%v'", changedFilesExtensionsKey, changedFilesExt, tt.want)
			}
		})
	}
}

func Test_getGiteaTokenSecret(t *testing.T) {

	ctx, _ := test.SetupFakeContext(t)
	var secretToken = "secret"

	type args struct {
		ctx context.Context
		r   *triggersv1.InterceptorRequest
		p   InterceptorParams
	}
	tests := []struct {
		name    string
		secret  *corev1.Secret
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "valid secret",
			args: args{
				ctx: ctx,
				r: &triggersv1.InterceptorRequest{
					Context: &triggersv1.TriggerContext{
						EventURL:  "https://testing.example.com",
						EventID:   "abcde",
						TriggerID: "namespaces/default/triggers/example-trigger",
					},
				},
				p: InterceptorParams{
					AddChangedFiles: AddChangedFiles{
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
							SecretKey:  "token",
						},
					},

					EventTypes: []string{"pull_request", "push"},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			want:    "secret",
			wantErr: false,
		},
		{
			name: "nil secret reference",
			args: args{
				ctx: ctx,
				r: &triggersv1.InterceptorRequest{
					Context: &triggersv1.TriggerContext{
						EventURL:  "https://testing.example.com",
						EventID:   "abcde",
						TriggerID: "namespaces/default/triggers/example-trigger",
					},
				},
				p: InterceptorParams{
					EventTypes: []string{"pull_request", "push"},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			want:    "",
			wantErr: false,
		},
		{
			name: "missing secret key, failure",
			args: args{
				ctx: ctx,
				r: &triggersv1.InterceptorRequest{
					Context: &triggersv1.TriggerContext{
						EventURL:  "https://testing.example.com",
						EventID:   "abcde",
						TriggerID: "namespaces/default/triggers/example-trigger",
					},
				},
				p: InterceptorParams{
					AddChangedFiles: AddChangedFiles{
						PersonalAccessToken: &triggersv1.SecretRef{
							SecretName: "mysecret",
						},
					},
					EventTypes: []string{"pull_request", "push"},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mysecret",
				},
				Data: map[string][]byte{
					"token": []byte(secretToken),
				},
			},
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			clientset := fakekubeclient.Get(ctx)
			if tt.secret != nil {
				tt.secret.Namespace = metav1.NamespaceDefault
				ctx, clientset = fakekubeclient.With(ctx, tt.secret)
			}

			w := &InterceptorImpl{
				SecretGetter: interceptors.DefaultSecretGetter(clientset.CoreV1()),
			}

			got, err := w.getGiteaTokenSecret(tt.args.ctx, tt.args.r, tt.args.p)
			if (err != nil) != tt.wantErr {
				t.Errorf("Interceptor.getSecret() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Interceptor.getSecret() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_makeClient(t *testing.T) {
	ctx, _ := test.SetupFakeContext(t)
	type args struct {
		ctx        context.Context
		insecure   bool
		baseURL    string
		caCertFile string
		token      string
		username   string
	}
	tests := []struct {
		name    string
		args    args
		want    *gitea.Client
		wantErr bool
	}{
		{
			name: "gitea",
			args: args{
				ctx:      ctx,
				insecure: false,
				baseURL:  "gitea.v3nc.org",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := makeClient(tt.args.ctx, tt.args.baseURL, tt.args.token, tt.args.username, tt.args.caCertFile, tt.args.insecure)
			if (err != nil) != tt.wantErr {
				t.Errorf("makeClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}
